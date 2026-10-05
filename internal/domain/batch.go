package domain

import (
	"sort"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type BatchStatus string

const (
	BatchActive    BatchStatus = "active"
	BatchCompleted BatchStatus = "completed"
	BatchCancelled BatchStatus = "cancelled"
)

// BatchMember 是批次对一份方案（及其标本）的不可变归属记录。
// 方案独立取消或完成时成员记录仍然保留，班组长可以据此还原整批操作的成员构成。
type BatchMember struct {
	PlanID     string    `json:"plan_id"`
	SpecimenID string    `json:"specimen_id"`
	JoinedAt   time.Time `json:"joined_at"`
}

type PrepBatch struct {
	ID          string        `json:"id"`
	SiteLabel   string        `json:"site_label"`
	Supervisor  string        `json:"supervisor"`
	Status      BatchStatus   `json:"status"`
	Members     []BatchMember `json:"members"`
	CancelledBy string        `json:"cancelled_by,omitempty"`
	CancelledAt *time.Time    `json:"cancelled_at,omitempty"`
	Version     int           `json:"version"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// NewPrepBatch 建立一次多标本制备批次。成员的开放状态与唯一性由编排层逐个校验，
// 构造函数只负责批次自身的最终不变量：至少一个成员、不重复、按方案标识有序。
func NewPrepBatch(
	id string,
	siteLabel string,
	supervisor string,
	members []BatchMember,
	now time.Time,
) (PrepBatch, error) {
	if id == "" {
		return PrepBatch{}, contracts.Internal("missing_generated_id", "批次标识未生成", nil)
	}
	site, err := requiredText("发掘地点", siteLabel, 120)
	if err != nil {
		return PrepBatch{}, err
	}
	lead, err := requiredText("负责人", supervisor, 80)
	if err != nil {
		return PrepBatch{}, err
	}
	if len(members) == 0 {
		return PrepBatch{}, contracts.Invalid("batch_without_members", "制备批次至少包含一份方案")
	}
	seenPlans := make(map[string]struct{}, len(members))
	seenSpecimens := make(map[string]struct{}, len(members))
	for _, member := range members {
		if member.PlanID == "" || member.SpecimenID == "" {
			return PrepBatch{}, contracts.Internal("invalid_batch_member", "批次成员缺少方案或标本标识", nil)
		}
		if _, dup := seenPlans[member.PlanID]; dup {
			return PrepBatch{}, contracts.Invalid("duplicate_batch_member", "同一方案不能重复加入制备批次")
		}
		if _, dup := seenSpecimens[member.SpecimenID]; dup {
			return PrepBatch{}, contracts.Invalid("duplicate_batch_specimen", "同一标本不能在制备批次中重复出现")
		}
		seenPlans[member.PlanID] = struct{}{}
		seenSpecimens[member.SpecimenID] = struct{}{}
	}
	ordered := append([]BatchMember(nil), members...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].PlanID < ordered[j].PlanID
	})
	stamp := now.UTC()
	return PrepBatch{
		ID:         id,
		SiteLabel:  site,
		Supervisor: lead,
		Status:     BatchActive,
		Members:    ordered,
		Version:    1,
		CreatedAt:  stamp,
		UpdatedAt:  stamp,
	}, nil
}

func (b PrepBatch) IsOpen() bool {
	return b.Status == BatchActive
}

func (b PrepBatch) HasMember(planID string) bool {
	for _, member := range b.Members {
		if member.PlanID == planID {
			return true
		}
	}
	return false
}

// MemberPlanIDs 返回当前批次的全部成员方案标识（有序）。
func (b PrepBatch) MemberPlanIDs() []string {
	ids := make([]string, 0, len(b.Members))
	for _, member := range b.Members {
		ids = append(ids, member.PlanID)
	}
	return ids
}

// RemoveMembers 用于批次拆分：成员记录整体迁入新批次，因此要从源批次移除。
// 取消或完成的成员是历史归属，不能通过拆分摘除。
func (b *PrepBatch) RemoveMembers(planIDs []string, plans map[string]PrepPlan, now time.Time) error {
	if b.Status != BatchActive {
		return contracts.Conflict("batch_not_open", "只有进行中的批次可以拆分")
	}
	if len(planIDs) == 0 {
		return contracts.Invalid("batch_split_without_members", "拆分批次至少要指定一份方案")
	}
	removing := make(map[string]struct{}, len(planIDs))
	for _, planID := range planIDs {
		if _, dup := removing[planID]; dup {
			return contracts.Invalid("duplicate_split_member", "拆分列表中存在重复方案")
		}
		removing[planID] = struct{}{}
		if !b.HasMember(planID) {
			return contracts.Conflict("batch_member_not_found", "指定方案不属于该制备批次")
		}
		plan, ok := plans[planID]
		if !ok {
			return contracts.NotFound("plan_not_found", "未找到修复方案 "+planID)
		}
		if !plan.IsOpen() {
			return contracts.Conflict("batch_member_not_open", "只有未结束的成员方案才能拆出批次")
		}
	}
	if len(removing) >= len(b.Members) {
		return contracts.Conflict("batch_split_empties_batch", "拆分必须在源批次保留至少一个成员")
	}
	remaining := make([]BatchMember, 0, len(b.Members)-len(removing))
	for _, member := range b.Members {
		if _, moved := removing[member.PlanID]; !moved {
			remaining = append(remaining, member)
		}
	}
	b.Members = remaining
	b.Version++
	b.UpdatedAt = now.UTC()
	return nil
}

// CompleteIfAllClosed 只有在每个成员方案都进入终态时才整体关闭批次；
// 单份标本完成时其余成员仍开放，批次保持 active。
func (b *PrepBatch) CompleteIfAllClosed(plans map[string]PrepPlan, now time.Time) (bool, error) {
	if b.Status != BatchActive {
		return false, nil
	}
	for _, member := range b.Members {
		plan, ok := plans[member.PlanID]
		if !ok {
			return false, contracts.NotFound("plan_not_found", "未找到修复方案 "+member.PlanID)
		}
		if plan.IsOpen() {
			return false, nil
		}
	}
	b.Status = BatchCompleted
	b.Version++
	b.UpdatedAt = now.UTC()
	return true, nil
}

// Cancel 取消批次只解除批次外壳：成员方案保留各自独立的状态，由编排层逐一分流。
func (b *PrepBatch) Cancel(cancelledBy string, now time.Time) error {
	if b.Status != BatchActive {
		return contracts.Conflict("batch_not_cancellable", "只有进行中的批次可以取消")
	}
	operator, err := requiredText("取消人", cancelledBy, 80)
	if err != nil {
		return err
	}
	stamp := now.UTC()
	b.Status = BatchCancelled
	b.CancelledBy = operator
	b.CancelledAt = &stamp
	b.Version++
	b.UpdatedAt = stamp
	return nil
}

// BatchStage 是一次批次进度写入共享的阶段事实：同一个阶段序号、名称、说明和记录人，
// 各标本据此生成独立的进度记录并保留各自的比例。
type BatchStage struct {
	ID         string    `json:"id"`
	BatchID    string    `json:"batch_id"`
	Sequence   int       `json:"sequence"`
	Stage      string    `json:"stage"`
	Note       string    `json:"note"`
	RecordedBy string    `json:"recorded_by"`
	CreatedAt  time.Time `json:"created_at"`
}

func NewBatchStage(
	id string,
	batch PrepBatch,
	sequence int,
	stage string,
	note string,
	recordedBy string,
	now time.Time,
) (BatchStage, error) {
	if id == "" {
		return BatchStage{}, contracts.Internal("missing_generated_id", "阶段标识未生成", nil)
	}
	if !batch.IsOpen() {
		return BatchStage{}, contracts.Conflict("batch_not_open", "只有进行中的批次可以记录共享阶段")
	}
	if sequence <= 0 {
		return BatchStage{}, contracts.Internal("invalid_stage_sequence", "阶段序号必须为正整数", nil)
	}
	stageValue, err := requiredText("阶段名称", stage, 80)
	if err != nil {
		return BatchStage{}, err
	}
	noteValue, err := optionalText("阶段说明", note, 500)
	if err != nil {
		return BatchStage{}, err
	}
	recorder, err := requiredText("记录人", recordedBy, 80)
	if err != nil {
		return BatchStage{}, err
	}
	return BatchStage{
		ID:         id,
		BatchID:    batch.ID,
		Sequence:   sequence,
		Stage:      stageValue,
		Note:       noteValue,
		RecordedBy: recorder,
		CreatedAt:  now.UTC(),
	}, nil
}
