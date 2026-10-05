package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// MemberView is one batch member with its independent plan state and latest
// percentage; the batch never overwrites these values.
type MemberView struct {
	Plan          domain.PrepPlan       `json:"plan"`
	SpecimenID    string                `json:"specimen_id"`
	LatestPercent int                   `json:"latest_percent"`
	LatestEntry   *domain.ProgressEntry `json:"latest_entry,omitempty"`
}

type StageView struct {
	Fact    domain.BatchStage      `json:"fact"`
	Entries []domain.ProgressEntry `json:"entries"`
}

type BatchSnapshot struct {
	Batch   domain.PrepBatch `json:"batch"`
	Members []MemberView     `json:"members"`
	Stages  []StageView      `json:"stages"`
}

// CreateBatch writes a preparation batch together with every member's new
// specimen and draft plan in one repository mutation. Any validation or
// conflict failure rolls back the whole write: no partial batch survives.
func (s *Service) CreateBatch(
	ctx context.Context,
	request contracts.CreateBatchRequest,
) (BatchSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BatchSnapshot{}, err
	}

	var snapshot BatchSnapshot
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		batch, err := domain.NewPrepBatch(
			s.nextID("batch"),
			request.Site,
			request.Purpose,
			request.Owner,
			len(request.Members),
			s.now(),
		)
		if err != nil {
			return err
		}
		seenCatalog := make(map[string]struct{}, len(request.Members))
		for _, spec := range request.Members {
			specimen, err := domain.NewSpecimen(
				s.nextID("specimen"),
				spec.CatalogNumber,
				spec.FieldLabel,
				spec.TaxonHint,
				spec.MatrixKind,
				spec.JacketNotes,
				s.now(),
			)
			if err != nil {
				return err
			}
			if _, duplicate := seenCatalog[specimen.CatalogNumber]; duplicate {
				return contracts.Conflict("duplicate_catalog_number", "批次内目录号重复："+specimen.CatalogNumber)
			}
			seenCatalog[specimen.CatalogNumber] = struct{}{}
			if _, exists := m.FindSpecimenByCatalog(specimen.CatalogNumber); exists {
				return contracts.Conflict("duplicate_catalog_number", "目录号已存在，不能覆盖原标本")
			}
			plan, err := domain.NewPrepPlan(
				s.nextID("plan"),
				specimen,
				spec.Objective,
				spec.Method,
				spec.Adhesive,
				spec.TargetExposure,
				s.now(),
			)
			if err != nil {
				return err
			}
			if err := specimen.MarkPlanned(s.now()); err != nil {
				return err
			}
			if err := plan.JoinBatch(batch.ID, s.now()); err != nil {
				return err
			}
			member, err := domain.NewBatchMember(batch, plan, s.now())
			if err != nil {
				return err
			}
			m.PutSpecimen(specimen)
			m.PutPlan(plan)
			m.AddBatchMember(member)
		}
		m.PutBatch(batch)
		snapshot = buildBatchSnapshot(m, batch)
		return nil
	})
	return snapshot, err
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
		snapshot = buildBatchSnapshot(m, batch)
		return nil
	})
	return snapshot, err
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

// ApproveBatch approves every draft member plan and moves each specimen to
// preparing together. A single member that is no longer a draft rejects the
// entire approval.
func (s *Service) ApproveBatch(
	ctx context.Context,
	batchID string,
	request contracts.BatchApprovalRequest,
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
		if err := batch.RequireOpen(); err != nil {
			return err
		}
		members := m.MembersForBatch(batch.ID)
		if len(members) == 0 {
			return contracts.Conflict("batch_without_members", "批次没有成员方案")
		}
		loaded := make([]domain.PrepPlan, 0, len(members))
		specimens := make([]domain.Specimen, 0, len(members))
		for _, member := range members {
			plan, ok := m.GetPlan(member.PlanID)
			if !ok {
				return notFoundPlan(member.PlanID)
			}
			if plan.BatchID != batch.ID {
				return contracts.Conflict("batch_member_mismatch", "成员方案已不属于该批次")
			}
			specimen, ok := m.GetSpecimen(plan.SpecimenID)
			if !ok {
				return notFoundSpecimen(plan.SpecimenID)
			}
			loaded = append(loaded, plan)
			specimens = append(specimens, specimen)
		}
		for i := range loaded {
			if err := loaded[i].Approve(request.ApprovedBy, s.now()); err != nil {
				return err
			}
			if err := specimens[i].MarkPreparing(s.now()); err != nil {
				return err
			}
		}
		for i := range loaded {
			m.PutPlan(loaded[i])
			m.PutSpecimen(specimens[i])
		}
		snapshot = buildBatchSnapshot(m, batch)
		return nil
	})
	return snapshot, err
}

// RecordBatchStage writes one shared stage fact plus an independent progress
// entry for each requested member in a single mutation. A member at 100% or a
// regression for any single member rejects the entire write.
func (s *Service) RecordBatchStage(
	ctx context.Context,
	batchID string,
	request contracts.RecordBatchStageRequest,
) (StageView, error) {
	if err := ctx.Err(); err != nil {
		return StageView{}, err
	}

	var view StageView
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		batch, ok := m.GetBatch(batchID)
		if !ok {
			return notFoundBatch(batchID)
		}
		if err := batch.RequireOpen(); err != nil {
			return err
		}
		previousStages := m.StagesForBatch(batch.ID)
		fact, err := domain.NewBatchStage(
			batch,
			m.NextStageID(batch.ID),
			previousStages,
			request.Stage,
			request.Note,
			request.RecordedBy,
			s.now(),
		)
		if err != nil {
			return err
		}

		seenPlans := make(map[string]struct{}, len(request.Entries))
		entries := make([]domain.ProgressEntry, 0, len(request.Entries))
		for _, spec := range request.Entries {
			if _, duplicate := seenPlans[spec.PlanID]; duplicate {
				return contracts.Conflict("batch_stage_duplicate_plan", "同一阶段事实中每个方案只能记录一次")
			}
			seenPlans[spec.PlanID] = struct{}{}

			plan, ok := m.GetPlan(spec.PlanID)
			if !ok {
				return notFoundPlan(spec.PlanID)
			}
			if plan.BatchID != batch.ID {
				return contracts.Conflict("batch_member_mismatch", "方案不属于该批次，不能写入共享阶段进度")
			}
			specimen, ok := m.GetSpecimen(plan.SpecimenID)
			if !ok {
				return notFoundSpecimen(plan.SpecimenID)
			}
			factRef := fact
			entry, err := s.appendProgress(
				m,
				plan,
				specimen,
				&batch,
				&factRef,
				fact.Stage,
				spec.Percent,
				spec.Note,
				request.RecordedBy,
			)
			if err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		m.AppendBatchStage(fact)
		view = StageView{Fact: fact, Entries: entries}
		return nil
	})
	return view, err
}

// CloseBatch finishes a batch only after every member plan has reached its
// own completed state. One unfinished member keeps the batch open, so a
// single specimen completing can never close the whole operation.
func (s *Service) CloseBatch(ctx context.Context, batchID string) (BatchSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BatchSnapshot{}, err
	}

	var snapshot BatchSnapshot
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		batch, ok := m.GetBatch(batchID)
		if !ok {
			return notFoundBatch(batchID)
		}
		if err := batch.RequireOpen(); err != nil {
			return err
		}
		for _, member := range m.MembersForBatch(batch.ID) {
			plan, ok := m.GetPlan(member.PlanID)
			if !ok {
				return notFoundPlan(member.PlanID)
			}
			if !plan.IsCompleted() {
				return contracts.Conflict("batch_has_open_members", "仍有成员方案未完成，不能关闭批次")
			}
		}
		if err := batch.Complete(s.now()); err != nil {
			return err
		}
		m.PutBatch(batch)
		snapshot = buildBatchSnapshot(m, batch)
		return nil
	})
	return snapshot, err
}

// CancelBatch cancels an open batch together with all of its draft plans. If
// a member has already been approved or started preparation, cancellation is
// rejected as a whole; members must first be split out to proceed
// independently.
func (s *Service) CancelBatch(
	ctx context.Context,
	batchID string,
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
		if err := batch.RequireOpen(); err != nil {
			return err
		}
		members := m.MembersForBatch(batch.ID)
		if len(members) == 0 {
			return contracts.Conflict("batch_without_members", "批次没有成员方案")
		}
		plans := make([]domain.PrepPlan, 0, len(members))
		for _, member := range members {
			plan, ok := m.GetPlan(member.PlanID)
			if !ok {
				return notFoundPlan(member.PlanID)
			}
			if plan.Status != domain.PlanDraft {
				return contracts.Conflict("batch_not_cancellable", "批次存在已审批或进行中的方案，不能整体取消")
			}
			plans = append(plans, plan)
		}
		for i := range plans {
			if err := plans[i].Cancel(s.now()); err != nil {
				return err
			}
			m.PutPlan(plans[i])
		}
		if err := batch.Cancel(s.now()); err != nil {
			return err
		}
		m.PutBatch(batch)
		snapshot = buildBatchSnapshot(m, batch)
		return nil
	})
	return snapshot, err
}

// SplitBatch moves the requested draft member plans into a newly created
// batch in one mutation. Unknown plans, non-draft plans, plans outside the
// source batch, or emptying the source batch all reject the whole split.
func (s *Service) SplitBatch(
	ctx context.Context,
	batchID string,
	request contracts.SplitBatchRequest,
) (BatchSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return BatchSnapshot{}, err
	}
	if len(request.PlanIDs) == 0 {
		return BatchSnapshot{}, contracts.Invalid("required_field", "拆分方案不能为空")
	}

	var snapshot BatchSnapshot
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		source, ok := m.GetBatch(batchID)
		if !ok {
			return notFoundBatch(batchID)
		}
		if err := source.RequireOpen(); err != nil {
			return err
		}
		current := m.MembersForBatch(source.ID)

		requested := make(map[string]struct{}, len(request.PlanIDs))
		for _, planID := range request.PlanIDs {
			if _, duplicate := requested[planID]; duplicate {
				return contracts.Conflict("batch_split_duplicate_plan", "拆分方案不能重复")
			}
			requested[planID] = struct{}{}
		}
		if len(current) <= len(requested) {
			return contracts.Conflict("batch_split_empties_source", "拆分后原批次必须至少保留一份标本")
		}

		target, err := domain.NewSplitBatch(
			s.nextID("batch"),
			source,
			source.Purpose+"（拆分）",
			len(requested),
			s.now(),
		)
		if err != nil {
			return err
		}

		for planID := range requested {
			plan, exists := m.GetPlan(planID)
			if !exists {
				return notFoundPlan(planID)
			}
			if plan.BatchID != source.ID {
				return contracts.Conflict("batch_member_mismatch", "方案不属于该批次，不能拆分")
			}
			if err := plan.LeaveBatch(s.now()); err != nil {
				return err
			}
			if err := plan.JoinBatch(target.ID, s.now()); err != nil {
				return err
			}
			member, err := domain.NewBatchMember(target, plan, s.now())
			if err != nil {
				return err
			}
			if !m.RemoveBatchMember(source.ID, plan.ID) {
				return contracts.Conflict("batch_member_mismatch", "方案不属于该批次，不能拆分")
			}
			m.PutPlan(plan)
			m.AddBatchMember(member)
		}
		m.PutBatch(target)
		snapshot = buildBatchSnapshot(m, target)
		return nil
	})
	return snapshot, err
}

// buildBatchSnapshot assembles the read projection for a batch inside a read
// or mutation closure. Member ordering follows join order; stage entries are
// grouped from each plan's independent progress trail.
func buildBatchSnapshot(m *repository.Mutation, batch domain.PrepBatch) BatchSnapshot {
	members := m.MembersForBatch(batch.ID)
	memberViews := make([]MemberView, 0, len(members))
	for _, member := range members {
		plan, ok := m.GetPlan(member.PlanID)
		if !ok {
			continue
		}
		entries := m.ProgressForPlan(plan.ID)
		view := MemberView{Plan: plan, SpecimenID: member.SpecimenID}
		if len(entries) > 0 {
			latest := entries[len(entries)-1]
			view.LatestPercent = latest.Percent
			view.LatestEntry = &latest
		}
		memberViews = append(memberViews, view)
	}
	stages := m.StagesForBatch(batch.ID)
	stageViews := make([]StageView, 0, len(stages))
	for _, fact := range stages {
		entries := make([]domain.ProgressEntry, 0)
		for _, member := range members {
			for _, entry := range m.ProgressForPlan(member.PlanID) {
				if entry.BatchStageID == fact.ID {
					entries = append(entries, entry)
				}
			}
		}
		stageViews = append(stageViews, StageView{Fact: fact, Entries: entries})
	}
	return BatchSnapshot{Batch: batch, Members: memberViews, Stages: stageViews}
}
