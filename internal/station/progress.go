package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) LogProgress(
	ctx context.Context,
	planID string,
	request contracts.ProgressRequest,
) (domain.ProgressEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProgressEntry{}, err
	}

	var created domain.ProgressEntry
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		var batch *domain.PrepBatch
		var fact *domain.BatchStage
		if plan.BatchID != "" {
			loaded, exists := m.GetBatch(plan.BatchID)
			if !exists {
				return contracts.Conflict("batch_not_found", "方案所属批次不存在")
			}
			batch = &loaded
		}
		if request.BatchStageID > 0 {
			if batch == nil {
				return contracts.Conflict("stage_fact_not_batched", "阶段事实只能用于批次内方案")
			}
			loaded, exists := m.GetBatchStage(batch.ID, request.BatchStageID)
			if !exists {
				return notFoundStage(batch.ID, request.BatchStageID)
			}
			fact = &loaded
		}
		entry, err := s.appendProgress(m, plan, specimen, batch, fact, request.Stage, request.Percent, request.Note, request.RecordedBy)
		if err != nil {
			return err
		}
		created = entry
		return nil
	})
	return created, err
}

// appendProgress validates and appends one progress entry inside an open
// mutation, advancing plan and specimen states in the same commit. The plan
// and specimen are values copied into the mutation snapshot; callers must
// read the returned entry, all persistence is performed here.
func (s *Service) appendProgress(
	m *repository.Mutation,
	plan domain.PrepPlan,
	specimen domain.Specimen,
	batch *domain.PrepBatch,
	fact *domain.BatchStage,
	stage string,
	percent int,
	note string,
	recordedBy string,
) (domain.ProgressEntry, error) {
	previous := m.ProgressForPlan(plan.ID)
	entry, err := domain.NewProgressEntry(
		s.nextID("progress"),
		plan,
		previous,
		percent,
		note,
		recordedBy,
		s.now(),
		domain.ProgressStageRef{
			Stage: stage,
			Batch: batch,
			Fact:  fact,
		},
	)
	if err != nil {
		return domain.ProgressEntry{}, err
	}
	if plan.Status == domain.PlanApproved {
		if err := plan.Begin(s.now()); err != nil {
			return domain.ProgressEntry{}, err
		}
	}
	if percent == 100 {
		if err := plan.Complete(s.now()); err != nil {
			return domain.ProgressEntry{}, err
		}
		if err := specimen.MarkStabilized(s.now()); err != nil {
			return domain.ProgressEntry{}, err
		}
	}
	m.AppendProgress(entry)
	m.PutPlan(plan)
	m.PutSpecimen(specimen)
	return entry, nil
}
