package domain

import (
	"fmt"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type ProgressEntry struct {
	ID           string    `json:"id"`
	PlanID       string    `json:"plan_id"`
	Sequence     int       `json:"sequence"`
	Stage        string    `json:"stage"`
	Percent      int       `json:"percent"`
	Note         string    `json:"note"`
	RecordedBy   string    `json:"recorded_by"`
	BatchID      string    `json:"batch_id,omitempty"`
	BatchStageID int       `json:"batch_stage_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ProgressStageRef optionally anchors an entry to a batch stage fact. When
// Fact is non-nil the entry shares the fact's stage name; otherwise Stage is
// used as a plan-local stage name.
type ProgressStageRef struct {
	Stage string
	Batch *PrepBatch
	Fact  *BatchStage
}

func NewProgressEntry(
	id string,
	plan PrepPlan,
	previous []ProgressEntry,
	percent int,
	note string,
	recordedBy string,
	now time.Time,
	ref ProgressStageRef,
) (ProgressEntry, error) {
	if id == "" {
		return ProgressEntry{}, contracts.Internal("missing_generated_id", "进度标识未生成", nil)
	}
	if plan.Status != PlanApproved && plan.Status != PlanActive {
		return ProgressEntry{}, contracts.Conflict("plan_not_recordable", "当前方案状态不能追加制备进度")
	}
	if err := validatePercent(percent); err != nil {
		return ProgressEntry{}, err
	}
	noteValue, err := optionalText("进度说明", note, 500)
	if err != nil {
		return ProgressEntry{}, err
	}
	recorder, err := requiredText("记录人", recordedBy, 80)
	if err != nil {
		return ProgressEntry{}, err
	}
	if len(previous) > 0 && percent < previous[len(previous)-1].Percent {
		return ProgressEntry{}, contracts.Conflict("progress_regressed", "新的进度比例不能低于上一条记录")
	}

	stageName := ref.Stage
	batchID := ""
	factID := 0
	switch {
	case ref.Fact != nil:
		if ref.Batch == nil || plan.BatchID == "" {
			return ProgressEntry{}, contracts.Conflict("stage_fact_not_batched", "阶段事实只能用于批次内方案")
		}
		if ref.Fact.BatchID != ref.Batch.ID || plan.BatchID != ref.Batch.ID {
			return ProgressEntry{}, contracts.Conflict("stage_fact_batch_mismatch", "阶段事实不属于该方案所在批次")
		}
		if err := ref.Batch.RequireOpen(); err != nil {
			return ProgressEntry{}, err
		}
		stageName = ref.Fact.Stage
		batchID = ref.Batch.ID
		factID = ref.Fact.ID
	case plan.BatchID != "":
		if ref.Batch == nil || ref.Batch.ID != plan.BatchID {
			return ProgressEntry{}, contracts.Internal("batch_not_loaded", "批次方案缺少批次上下文", nil)
		}
		if err := ref.Batch.RequireOpen(); err != nil {
			return ProgressEntry{}, err
		}
		validated, err := requiredText("阶段名称", stageName, 80)
		if err != nil {
			return ProgressEntry{}, err
		}
		stageName = validated
		batchID = ref.Batch.ID
	default:
		validated, err := requiredText("阶段名称", stageName, 80)
		if err != nil {
			return ProgressEntry{}, err
		}
		stageName = validated
	}

	return ProgressEntry{
		ID:           id,
		PlanID:       plan.ID,
		Sequence:     len(previous) + 1,
		Stage:        stageName,
		Percent:      percent,
		Note:         noteValue,
		RecordedBy:   recorder,
		BatchID:      batchID,
		BatchStageID: factID,
		CreatedAt:    now.UTC(),
	}, nil
}

func (p ProgressEntry) String() string {
	return fmt.Sprintf("%s#%d(%d%%)", p.PlanID, p.Sequence, p.Percent)
}
