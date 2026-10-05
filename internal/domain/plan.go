package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type PlanStatus string

const (
	PlanDraft     PlanStatus = "draft"
	PlanApproved  PlanStatus = "approved"
	PlanActive    PlanStatus = "active"
	PlanCompleted PlanStatus = "completed"
	PlanCancelled PlanStatus = "cancelled"
)

type PrepPlan struct {
	ID             string     `json:"id"`
	SpecimenID     string     `json:"specimen_id"`
	Objective      string     `json:"objective"`
	Method         string     `json:"method"`
	Adhesive       string     `json:"adhesive"`
	TargetExposure int        `json:"target_exposure"`
	Status         PlanStatus `json:"status"`
	ApprovedBy     string     `json:"approved_by,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
	CancelledBy    string     `json:"cancelled_by,omitempty"`
	BatchID        string     `json:"batch_id,omitempty"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func NewPrepPlan(
	id string,
	specimen Specimen,
	objective string,
	method string,
	adhesive string,
	targetExposure int,
	now time.Time,
) (PrepPlan, error) {
	if id == "" {
		return PrepPlan{}, contracts.Internal("missing_generated_id", "方案标识未生成", nil)
	}
	if !specimen.CanCreatePlan() {
		return PrepPlan{}, contracts.Conflict("specimen_not_plannable", "当前标本状态不能创建修复方案")
	}
	goal, err := requiredText("修复目标", objective, 200)
	if err != nil {
		return PrepPlan{}, err
	}
	approach, err := requiredText("修复方法", method, 160)
	if err != nil {
		return PrepPlan{}, err
	}
	adhesiveValue, err := optionalText("黏结材料", adhesive, 120)
	if err != nil {
		return PrepPlan{}, err
	}
	if err := validatePercent(targetExposure); err != nil {
		return PrepPlan{}, contracts.Invalid("invalid_target_exposure", "目标暴露比例必须位于 0 到 100 之间")
	}
	if targetExposure == 0 {
		return PrepPlan{}, contracts.Invalid("invalid_target_exposure", "目标暴露比例必须大于零")
	}
	return PrepPlan{
		ID:             id,
		SpecimenID:     specimen.ID,
		Objective:      goal,
		Method:         approach,
		Adhesive:       adhesiveValue,
		TargetExposure: targetExposure,
		Status:         PlanDraft,
		Version:        1,
		CreatedAt:      now.UTC(),
		UpdatedAt:      now.UTC(),
	}, nil
}

func (p PrepPlan) IsOpen() bool {
	return p.Status == PlanDraft || p.Status == PlanApproved || p.Status == PlanActive
}

func (p PrepPlan) IsCompleted() bool {
	return p.Status == PlanCompleted
}

// JoinBatch 把方案挂到进行中的制备批次上；已终态或已属于其他进行中批次的方案拒绝挂载。
func (p *PrepPlan) JoinBatch(batchID string, now time.Time) error {
	if batchID == "" {
		return contracts.Internal("missing_batch_id", "批次标识未生成", nil)
	}
	if !p.IsOpen() {
		return contracts.Conflict("plan_not_open_for_batch", "只有未结束的方案才能加入制备批次")
	}
	if p.BatchID != "" && p.BatchID != batchID {
		return contracts.Conflict("plan_already_batched", "该方案已属于另一个进行中的制备批次")
	}
	p.BatchID = batchID
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}

// LeaveBatch 在批次取消或拆分时解除方案与进行中批次的归属，方案本身的状态保持不变。
func (p *PrepPlan) LeaveBatch(batchID string, now time.Time) {
	if p.BatchID == batchID {
		p.BatchID = ""
		p.UpdatedAt = now.UTC()
	}
}

func (p *PrepPlan) Approve(approvedBy string, now time.Time) error {
	if p.Status != PlanDraft {
		return contracts.Conflict("plan_not_approvable", "只有草稿方案可以审批")
	}
	approver, err := requiredText("审批人", approvedBy, 80)
	if err != nil {
		return err
	}
	stamp := now.UTC()
	p.Status = PlanApproved
	p.ApprovedBy = approver
	p.ApprovedAt = &stamp
	p.Version++
	p.UpdatedAt = stamp
	return nil
}

func (p *PrepPlan) Begin(now time.Time) error {
	if p.Status == PlanActive {
		return nil
	}
	if p.Status != PlanApproved {
		return contracts.Conflict("plan_not_active", "只有已审批方案可以开始制备")
	}
	p.Status = PlanActive
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *PrepPlan) Complete(now time.Time) error {
	if p.Status == PlanCompleted {
		return nil
	}
	if p.Status != PlanActive {
		return contracts.Conflict("plan_not_completable", "只有进行中的方案可以完成")
	}
	p.Status = PlanCompleted
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *PrepPlan) Cancel(cancelledBy string, now time.Time) error {
	if p.Status != PlanDraft {
		return contracts.Conflict("plan_not_cancellable", "只有草稿方案可以取消")
	}
	operator, err := requiredText("取消人", cancelledBy, 80)
	if err != nil {
		return err
	}
	p.Status = PlanCancelled
	p.CancelledBy = operator
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}
