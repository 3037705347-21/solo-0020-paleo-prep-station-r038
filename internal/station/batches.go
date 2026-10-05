package station

import (
	"context"
	"sort"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// BatchMemberView 投影单个成员方案在批次中的独立事实：自身状态、最新比例和记录条数。
type BatchMemberView struct {
	PlanID        string            `json:"plan_id"`
	SpecimenID    string            `json:"specimen_id"`
	PlanStatus    domain.PlanStatus `json:"plan_status"`
	LatestPercent int               `json:"latest_percent"`
	ProgressCount int               `json:"progress_count"`
	JoinedAt      time.Time         `json:"joined_at"`
}

// BatchSnapshot 同时给出批次外壳、成员独立事实和共享阶段事实。
type BatchSnapshot struct {
	Batch   domain.PrepBatch    `json:"batch"`
	Members []BatchMemberView   `json:"members"`
	Stages  []domain.BatchStage `json:"stages"`
}

func (s *Service) CreateBatch(
	ctx context.Context,
	request contracts.CreateBatchRequest,
) (domain.PrepBatch, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepBatch{}, err
	}

	var created domain.PrepBatch
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		planIDs := request.PlanIDs
		if len(planIDs) < 2 {
			return contracts.Invalid("batch_requires_multiple", "多标本制备批次至少包含两份方案")
		}
		seen := make(map[string]struct{}, len(planIDs))
		members := make([]domain.BatchMember, 0, len(planIDs))
		plans := make(map[string]domain.PrepPlan, len(planIDs))
		stamp := s.now()
		for _, planID := range planIDs {
			if planID == "" {
				return contracts.Invalid("required_field", "方案标识不能为空")
			}
			if _, dup := seen[planID]; dup {
				return contracts.Invalid("duplicate_batch_member", "同一方案不能重复加入制备批次")
			}
			seen[planID] = struct{}{}
			plan, ok := m.GetPlan(planID)
			if !ok {
				return notFoundPlan(planID)
			}
			if !plan.IsOpen() {
				return contracts.Conflict("plan_not_open_for_batch", "只有未结束的方案才能加入制备批次")
			}
			if _, exists := m.GetSpecimen(plan.SpecimenID); !exists {
				return notFoundSpecimen(plan.SpecimenID)
			}
			if _, exists := m.OpenBatchForPlan(planID); exists {
				return contracts.Conflict("plan_already_batched", "该方案已属于另一个进行中的制备批次")
			}
			plans[planID] = plan
			members = append(members, domain.BatchMember{
				PlanID:     plan.ID,
				SpecimenID: plan.SpecimenID,
				JoinedAt:   stamp.UTC(),
			})
		}
		batch, err := domain.NewPrepBatch(s.nextID("batch"), request.SiteLabel, request.Supervisor, members, stamp)
		if err != nil {
			return err
		}
		for _, planID := range batch.MemberPlanIDs() {
			plan := plans[planID]
			if err := plan.JoinBatch(batch.ID, stamp); err != nil {
				return err
			}
			m.PutPlan(plan)
		}
		m.PutBatch(batch)
		created = batch
		return nil
	})
	return created, err
}

func (s *Service) ListBatches(ctx context.Context) ([]domain.PrepBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.PrepBatch
	err := s.repo.Read(func(m *repository.Mutation) error {
		values = m.ListBatches()
		return nil
	})
	return values, err
}

func (s *Service) GetBatch(ctx context.Context, batchID string) (BatchSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BatchSnapshot{}, err
	}
	var snapshot BatchSnapshot
	err := s.repo.Read(func(m *repository.Mutation) error {
		batch, ok := m.GetBatch(batchID)
		if !ok {
			return notFoundBatch(batchID)
		}
		members := make([]BatchMemberView, 0, len(batch.Members))
		for _, member := range batch.Members {
			view := BatchMemberView{
				PlanID:     member.PlanID,
				SpecimenID: member.SpecimenID,
				JoinedAt:   member.JoinedAt,
			}
			if plan, ok := m.GetPlan(member.PlanID); ok {
				view.PlanStatus = plan.Status
			}
			previous := m.ProgressForPlan(member.PlanID)
			view.ProgressCount = len(previous)
			if len(previous) > 0 {
				view.LatestPercent = previous[len(previous)-1].Percent
			}
			members = append(members, view)
		}
		snapshot = BatchSnapshot{
			Batch:   batch,
			Members: members,
			Stages:  m.StagesForBatch(batchID),
		}
		return nil
	})
	return snapshot, err
}

type batchItemWork struct {
	plan     domain.PrepPlan
	specimen domain.Specimen
	entry    domain.ProgressEntry
}

// LogBatchProgress 在同一个仓储事务内为整批写入共享阶段事实。
// 任一条标本进度不合法都会中止整个事务，批次阶段和任何标本进度都不会落库。
func (s *Service) LogBatchProgress(
	ctx context.Context,
	batchID string,
	request contracts.BatchProgressRequest,
) (BatchSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BatchSnapshot{}, err
	}

	var snapshot BatchSnapshot
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		batch, ok := m.GetBatch(batchID)
		if !ok {
			return notFoundBatch(batchID)
		}
		if !batch.IsOpen() {
			return contracts.Conflict("batch_not_open", "只有进行中的批次可以记录共享阶段")
		}
		if len(request.Items) == 0 {
			return contracts.Invalid("batch_progress_without_items", "批次进度必须至少包含一份标本")
		}

		recording := make(map[string]int, len(request.Items))
		for index, item := range request.Items {
			if item.PlanID == "" {
				return contracts.Invalid("required_field", "方案标识不能为空")
			}
			if _, dup := recording[item.PlanID]; dup {
				return contracts.Invalid("duplicate_batch_progress_item", "同一方案在同一阶段只能出现一次")
			}
			recording[item.PlanID] = index
			if !batch.HasMember(item.PlanID) {
				return contracts.Conflict("batch_member_not_found", "指定方案不属于该制备批次")
			}
		}

		// 开放成员必须全部到齐；已完成或已取消的成员不参与后续阶段。
		openMembers := make(map[string]struct{})
		for _, member := range batch.Members {
			plan, ok := m.GetPlan(member.PlanID)
			if !ok {
				return notFoundPlan(member.PlanID)
			}
			if plan.IsOpen() {
				openMembers[member.PlanID] = struct{}{}
			}
		}
		if len(recording) != len(openMembers) {
			return contracts.Conflict("batch_progress_members_mismatch", "批次进度必须覆盖当前全部未结束成员")
		}
		for planID := range openMembers {
			if _, present := recording[planID]; !present {
				return contracts.Conflict("batch_progress_members_mismatch", "批次进度必须覆盖当前全部未结束成员")
			}
		}

		stamp := s.now()
		stages := m.StagesForBatch(batchID)
		stage, err := domain.NewBatchStage(
			s.nextID("stage"),
			batch,
			len(stages)+1,
			request.Stage,
			request.Note,
			request.RecordedBy,
			stamp,
		)
		if err != nil {
			return err
		}

		// 先按方案标识排序构造全部变更，保证失败回滚和成功提交的结果都确定。
		planIDs := make([]string, 0, len(recording))
		for planID := range recording {
			planIDs = append(planIDs, planID)
		}
		sort.Strings(planIDs)
		works := make([]batchItemWork, 0, len(planIDs))
		for _, planID := range planIDs {
			plan, ok := m.GetPlan(planID)
			if !ok {
				return notFoundPlan(planID)
			}
			specimen, ok := m.GetSpecimen(plan.SpecimenID)
			if !ok {
				return notFoundSpecimen(plan.SpecimenID)
			}
			percent := request.Items[recording[planID]].Percent
			entry, err := domain.NewProgressEntry(
				s.nextID("progress"),
				plan,
				m.ProgressForPlan(planID),
				domain.ProgressLink{BatchID: batch.ID, StageID: stage.ID},
				request.Stage,
				percent,
				request.Note,
				request.RecordedBy,
				stamp,
			)
			if err != nil {
				return err
			}
			if plan.Status == domain.PlanApproved {
				if err := plan.Begin(stamp); err != nil {
					return err
				}
			}
			if percent == 100 {
				if err := plan.Complete(stamp); err != nil {
					return err
				}
				if err := specimen.MarkStabilized(stamp); err != nil {
					return err
				}
			}
			works = append(works, batchItemWork{plan: plan, specimen: specimen, entry: entry})
		}

		m.AppendStage(stage)
		for _, work := range works {
			m.AppendProgress(work.entry)
			m.PutPlan(work.plan)
			m.PutSpecimen(work.specimen)
		}
		// 闭合判定必须看到全部成员（包括本次未参与的已终态成员）。
		allPlans := make(map[string]domain.PrepPlan, len(batch.Members))
		for _, member := range batch.Members {
			plan, ok := m.GetPlan(member.PlanID)
			if !ok {
				return notFoundPlan(member.PlanID)
			}
			allPlans[member.PlanID] = plan
		}
		// 只有全部成员方案终态时批次才整体关闭；部分完成保持 active。
		if _, err := batch.CompleteIfAllClosed(allPlans, stamp); err != nil {
			return err
		}
		m.PutBatch(batch)
		snapshot = buildBatchSnapshot(m, batch)
		return nil
	})
	return snapshot, err
}

// SplitBatch 把仍开放的成员方案确定性地迁入新批次，源批次至少保留一个成员。
func (s *Service) SplitBatch(
	ctx context.Context,
	batchID string,
	request contracts.SplitBatchRequest,
) (domain.PrepBatch, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepBatch{}, err
	}

	var created domain.PrepBatch
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		source, ok := m.GetBatch(batchID)
		if !ok {
			return notFoundBatch(batchID)
		}
		if len(request.PlanIDs) == 0 {
			return contracts.Invalid("batch_split_without_members", "拆分批次至少要指定一份方案")
		}
		unique := make(map[string]struct{}, len(request.PlanIDs))
		for _, planID := range request.PlanIDs {
			if planID == "" {
				return contracts.Invalid("required_field", "方案标识不能为空")
			}
			if _, dup := unique[planID]; dup {
				return contracts.Invalid("duplicate_split_member", "拆分列表中存在重复方案")
			}
			unique[planID] = struct{}{}
		}

		plans := make(map[string]domain.PrepPlan, len(source.Members))
		for _, member := range source.Members {
			plan, ok := m.GetPlan(member.PlanID)
			if !ok {
				return notFoundPlan(member.PlanID)
			}
			plans[member.PlanID] = plan
		}
		stamp := s.now()
		if err := source.RemoveMembers(request.PlanIDs, plans, stamp); err != nil {
			return err
		}

		siteLabel := request.SiteLabel
		if siteLabel == "" {
			siteLabel = source.SiteLabel
		}
		movedMembers := make([]domain.BatchMember, 0, len(request.PlanIDs))
		movedPlans := make(map[string]domain.PrepPlan, len(request.PlanIDs))
		for _, planID := range request.PlanIDs {
			plan := plans[planID]
			movedMembers = append(movedMembers, domain.BatchMember{
				PlanID:     plan.ID,
				SpecimenID: plan.SpecimenID,
				JoinedAt:   stamp.UTC(),
			})
			movedPlans[planID] = plan
		}
		target, err := domain.NewPrepBatch(s.nextID("batch"), siteLabel, source.Supervisor, movedMembers, stamp)
		if err != nil {
			return err
		}
		for _, planID := range request.PlanIDs {
			plan := movedPlans[planID]
			plan.LeaveBatch(source.ID, stamp)
			if err := plan.JoinBatch(target.ID, stamp); err != nil {
				return err
			}
			m.PutPlan(plan)
		}
		m.PutBatch(source)
		m.PutBatch(target)
		created = target
		return nil
	})
	return created, err
}

// CancelBatch 取消批次外壳：开放成员脱离批次后继续按独立方案流转；
// 已终态成员保留批次指针作为历史归属，批次与其共享阶段记录永久保留。
func (s *Service) CancelBatch(
	ctx context.Context,
	batchID string,
	request contracts.CancelBatchRequest,
) (domain.PrepBatch, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepBatch{}, err
	}

	var cancelled domain.PrepBatch
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		batch, ok := m.GetBatch(batchID)
		if !ok {
			return notFoundBatch(batchID)
		}
		stamp := s.now()
		if err := batch.Cancel(request.CancelledBy, stamp); err != nil {
			return err
		}
		for _, member := range batch.Members {
			plan, ok := m.GetPlan(member.PlanID)
			if !ok {
				return notFoundPlan(member.PlanID)
			}
			if plan.IsOpen() {
				plan.LeaveBatch(batch.ID, stamp)
				m.PutPlan(plan)
			}
		}
		m.PutBatch(batch)
		cancelled = batch
		return nil
	})
	return cancelled, err
}

// CancelPlan 取消单份草稿方案；若它是某进行中批次的成员，批次在同一事务内重算闭合状态。
func (s *Service) CancelPlan(
	ctx context.Context,
	planID string,
	request contracts.CancelPlanRequest,
) (domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepPlan{}, err
	}

	var cancelled domain.PrepPlan
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		stamp := s.now()
		if err := plan.Cancel(request.CancelledBy, stamp); err != nil {
			return err
		}
		m.PutPlan(plan)
		if batch, isMember := m.OpenBatchForPlan(planID); isMember {
			plans := make(map[string]domain.PrepPlan, len(batch.Members))
			for _, member := range batch.Members {
				memberPlan, ok := m.GetPlan(member.PlanID)
				if !ok {
					return notFoundPlan(member.PlanID)
				}
				plans[member.PlanID] = memberPlan
			}
			if _, err := batch.CompleteIfAllClosed(plans, stamp); err != nil {
				return err
			}
			m.PutBatch(batch)
		}
		cancelled = plan
		return nil
	})
	return cancelled, err
}

func buildBatchSnapshot(m *repository.Mutation, batch domain.PrepBatch) BatchSnapshot {
	members := make([]BatchMemberView, 0, len(batch.Members))
	for _, member := range batch.Members {
		view := BatchMemberView{
			PlanID:     member.PlanID,
			SpecimenID: member.SpecimenID,
			JoinedAt:   member.JoinedAt,
		}
		if plan, ok := m.GetPlan(member.PlanID); ok {
			view.PlanStatus = plan.Status
		}
		previous := m.ProgressForPlan(member.PlanID)
		view.ProgressCount = len(previous)
		if len(previous) > 0 {
			view.LatestPercent = previous[len(previous)-1].Percent
		}
		members = append(members, view)
	}
	return BatchSnapshot{
		Batch:   batch,
		Members: members,
		Stages:  m.StagesForBatch(batch.ID),
	}
}
