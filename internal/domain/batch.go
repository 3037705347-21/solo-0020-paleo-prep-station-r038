package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type BatchStatus string

const (
	BatchOpen      BatchStatus = "open"
	BatchCompleted BatchStatus = "completed"
	BatchCancelled BatchStatus = "cancelled"
)

const (
	MinBatchMembers = 2
	MaxBatchMembers = 50
)

// PrepBatch groups several independent specimen plans into one preparation
// operation. The batch only carries shared stage facts and lifecycle; each
// member keeps its own plan, percentages and terminal state.
type PrepBatch struct {
	ID         string      `json:"id"`
	Site       string      `json:"site"`
	Purpose    string      `json:"purpose"`
	CreatedBy  string      `json:"created_by"`
	Status     BatchStatus `json:"status"`
	Version    int         `json:"version"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
}

// BatchMember binds one plan to a batch. Membership changes (creation and
// split) are the only places this row is written.
type BatchMember struct {
	BatchID    string    `json:"batch_id"`
	PlanID     string    `json:"plan_id"`
	SpecimenID string    `json:"specimen_id"`
	JoinedAt   time.Time `json:"joined_at"`
}

// BatchStage is a stage fact shared by one preparation operation. Every
// member may reference the same fact from its own ordered progress entries
// while still recording its own completion percentage.
type BatchStage struct {
	ID         int       `json:"id"`
	BatchID    string    `json:"batch_id"`
	Sequence   int       `json:"sequence"`
	Stage      string    `json:"stage"`
	Note       string    `json:"note"`
	RecordedBy string    `json:"recorded_by"`
	CreatedAt  time.Time `json:"created_at"`
}

func NewPrepBatch(
	id string,
	site string,
	purpose string,
	createdBy string,
	memberCount int,
	now time.Time,
) (PrepBatch, error) {
	if memberCount < MinBatchMembers {
		return PrepBatch{}, contracts.Invalid("batch_too_small", "一次制备批次至少包含两份标本")
	}
	if memberCount > MaxBatchMembers {
		return PrepBatch{}, contracts.Invalid("batch_too_large", "一次制备批次最多包含五十份标本")
	}
	return newBatch(id, site, purpose, createdBy, now)
}

// NewSplitBatch creates the destination batch for a split. A split may move a
// single remaining specimen, so the minimum member count does not apply; the
// maximum still does.
func NewSplitBatch(
	id string,
	source PrepBatch,
	purpose string,
	memberCount int,
	now time.Time,
) (PrepBatch, error) {
	if memberCount < 1 {
		return PrepBatch{}, contracts.Invalid("required_field", "拆分批次必须至少包含一份标本")
	}
	if memberCount > MaxBatchMembers {
		return PrepBatch{}, contracts.Invalid("batch_too_large", "一次制备批次最多包含五十份标本")
	}
	return newBatch(id, source.Site, purpose, source.CreatedBy, now)
}

func newBatch(
	id string,
	site string,
	purpose string,
	createdBy string,
	now time.Time,
) (PrepBatch, error) {
	if id == "" {
		return PrepBatch{}, contracts.Internal("missing_generated_id", "批次标识未生成", nil)
	}
	siteValue, err := requiredText("出土地点", site, 120)
	if err != nil {
		return PrepBatch{}, err
	}
	purposeValue, err := requiredText("批次目的", purpose, 200)
	if err != nil {
		return PrepBatch{}, err
	}
	creator, err := requiredText("负责人", createdBy, 80)
	if err != nil {
		return PrepBatch{}, err
	}
	stamp := now.UTC()
	return PrepBatch{
		ID:        id,
		Site:      siteValue,
		Purpose:   purposeValue,
		CreatedBy: creator,
		Status:    BatchOpen,
		Version:   1,
		CreatedAt: stamp,
		UpdatedAt: stamp,
	}, nil
}

func (b PrepBatch) IsOpen() bool {
	return b.Status == BatchOpen
}

func (b PrepBatch) IsFinished() bool {
	return b.Status == BatchCompleted || b.Status == BatchCancelled
}

// RequireOpen guards every mutation that acts on batch contents.
func (b PrepBatch) RequireOpen() error {
	if b.Status != BatchOpen {
		return contracts.Conflict("batch_not_open", "批次已经结束，不能继续变更")
	}
	return nil
}

func (b *PrepBatch) Complete(now time.Time) error {
	if err := b.RequireOpen(); err != nil {
		return err
	}
	stamp := now.UTC()
	b.Status = BatchCompleted
	b.Version++
	b.UpdatedAt = stamp
	b.FinishedAt = &stamp
	return nil
}

func (b *PrepBatch) Cancel(now time.Time) error {
	if err := b.RequireOpen(); err != nil {
		return err
	}
	stamp := now.UTC()
	b.Status = BatchCancelled
	b.Version++
	b.UpdatedAt = stamp
	b.FinishedAt = &stamp
	return nil
}

func NewBatchMember(batch PrepBatch, plan PrepPlan, now time.Time) (BatchMember, error) {
	if err := batch.RequireOpen(); err != nil {
		return BatchMember{}, err
	}
	if plan.Status != PlanDraft {
		return BatchMember{}, contracts.Conflict("plan_not_batchable", "只有草稿方案可以加入制备批次")
	}
	if plan.BatchID != "" && plan.BatchID != batch.ID {
		return BatchMember{}, contracts.Conflict("plan_already_batched", "该方案已经属于另一个制备批次")
	}
	return BatchMember{
		BatchID:    batch.ID,
		PlanID:     plan.ID,
		SpecimenID: plan.SpecimenID,
		JoinedAt:   now.UTC(),
	}, nil
}

func NewBatchStage(
	batch PrepBatch,
	id int,
	previous []BatchStage,
	stage string,
	note string,
	recordedBy string,
	now time.Time,
) (BatchStage, error) {
	if err := batch.RequireOpen(); err != nil {
		return BatchStage{}, err
	}
	if id <= 0 {
		return BatchStage{}, contracts.Internal("missing_generated_id", "阶段事实标识未生成", nil)
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
		Sequence:   len(previous) + 1,
		Stage:      stageValue,
		Note:       noteValue,
		RecordedBy: recorder,
		CreatedAt:  now.UTC(),
	}, nil
}
