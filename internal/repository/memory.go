package repository

import (
	"sort"
	"sync"

	"example.com/paleo-prep-station/internal/domain"
)

type state struct {
	specimens map[string]domain.Specimen
	plans     map[string]domain.PrepPlan
	progress  map[string][]domain.ProgressEntry
	releases  map[string]domain.ReleaseRecord
	batches   map[string]domain.PrepBatch
	members   map[string][]domain.BatchMember // batch id -> members
	stages    map[string][]domain.BatchStage  // batch id -> ordered stage facts
	stageSeq  map[string]int                  // batch id -> last stage numeric id
}

type MemoryRepository struct {
	mu    sync.Mutex
	state state
}

func NewMemory() *MemoryRepository {
	return &MemoryRepository{
		state: state{
			specimens: make(map[string]domain.Specimen),
			plans:     make(map[string]domain.PrepPlan),
			progress:  make(map[string][]domain.ProgressEntry),
			releases:  make(map[string]domain.ReleaseRecord),
			batches:   make(map[string]domain.PrepBatch),
			members:   make(map[string][]domain.BatchMember),
			stages:    make(map[string][]domain.BatchStage),
			stageSeq:  make(map[string]int),
		},
	}
}

func (r *MemoryRepository) Read(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(cloneMutation(r.state))
}

func (r *MemoryRepository) Mutate(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx := cloneMutation(r.state)
	if err := fn(tx); err != nil {
		return err
	}
	r.state = tx.snapshot()
	return nil
}

func cloneMutation(source state) *Mutation {
	copied := state{
		specimens: make(map[string]domain.Specimen, len(source.specimens)),
		plans:     make(map[string]domain.PrepPlan, len(source.plans)),
		progress:  make(map[string][]domain.ProgressEntry, len(source.progress)),
		releases:  make(map[string]domain.ReleaseRecord, len(source.releases)),
		batches:   make(map[string]domain.PrepBatch, len(source.batches)),
		members:   make(map[string][]domain.BatchMember, len(source.members)),
		stages:    make(map[string][]domain.BatchStage, len(source.stages)),
		stageSeq:  make(map[string]int, len(source.stageSeq)),
	}
	for key, value := range source.specimens {
		copied.specimens[key] = value
	}
	for key, value := range source.plans {
		copied.plans[key] = value
	}
	for key, values := range source.progress {
		copied.progress[key] = append([]domain.ProgressEntry(nil), values...)
	}
	for key, value := range source.releases {
		copied.releases[key] = value
	}
	for key, value := range source.batches {
		copied.batches[key] = value
	}
	for key, values := range source.members {
		copied.members[key] = append([]domain.BatchMember(nil), values...)
	}
	for key, values := range source.stages {
		copied.stages[key] = append([]domain.BatchStage(nil), values...)
	}
	for key, value := range source.stageSeq {
		copied.stageSeq[key] = value
	}
	return &Mutation{state: copied}
}

type Mutation struct {
	state state
}

func (m *Mutation) snapshot() state {
	return m.state
}

func (m *Mutation) GetSpecimen(id string) (domain.Specimen, bool) {
	value, ok := m.state.specimens[id]
	return value, ok
}

func (m *Mutation) FindSpecimenByCatalog(catalogNumber string) (domain.Specimen, bool) {
	for _, value := range m.state.specimens {
		if value.CatalogNumber == catalogNumber {
			return value, true
		}
	}
	return domain.Specimen{}, false
}

func (m *Mutation) ListSpecimens() []domain.Specimen {
	values := make([]domain.Specimen, 0, len(m.state.specimens))
	for _, value := range m.state.specimens {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutSpecimen(value domain.Specimen) {
	m.state.specimens[value.ID] = value
}

func (m *Mutation) GetPlan(id string) (domain.PrepPlan, bool) {
	value, ok := m.state.plans[id]
	return value, ok
}

func (m *Mutation) ListPlansForSpecimen(specimenID string) []domain.PrepPlan {
	values := make([]domain.PrepPlan, 0)
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutPlan(value domain.PrepPlan) {
	m.state.plans[value.ID] = value
}

func (m *Mutation) HasOpenPlan(specimenID string) bool {
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID && value.IsOpen() {
			return true
		}
	}
	return false
}

func (m *Mutation) ProgressForPlan(planID string) []domain.ProgressEntry {
	return append([]domain.ProgressEntry(nil), m.state.progress[planID]...)
}

func (m *Mutation) AppendProgress(value domain.ProgressEntry) {
	m.state.progress[value.PlanID] = append(m.state.progress[value.PlanID], value)
}

func (m *Mutation) ReleaseForSpecimen(specimenID string) (domain.ReleaseRecord, bool) {
	for _, value := range m.state.releases {
		if value.SpecimenID == specimenID {
			return value, true
		}
	}
	return domain.ReleaseRecord{}, false
}

func (m *Mutation) PutRelease(value domain.ReleaseRecord) {
	m.state.releases[value.ID] = value
}

// --- batches ---

func (m *Mutation) GetBatch(id string) (domain.PrepBatch, bool) {
	value, ok := m.state.batches[id]
	return value, ok
}

func (m *Mutation) PutBatch(value domain.PrepBatch) {
	m.state.batches[value.ID] = value
	if _, ok := m.state.members[value.ID]; !ok {
		m.state.members[value.ID] = nil
	}
	if _, ok := m.state.stages[value.ID]; !ok {
		m.state.stages[value.ID] = nil
	}
}

func (m *Mutation) ListBatches() []domain.PrepBatch {
	values := make([]domain.PrepBatch, 0, len(m.state.batches))
	for _, value := range m.state.batches {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

// MembersForBatch returns batch members in stable join order.
func (m *Mutation) MembersForBatch(batchID string) []domain.BatchMember {
	values := append([]domain.BatchMember(nil), m.state.members[batchID]...)
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].JoinedAt.Equal(values[j].JoinedAt) {
			return values[i].PlanID < values[j].PlanID
		}
		return values[i].JoinedAt.Before(values[j].JoinedAt)
	})
	return values
}

func (m *Mutation) AddBatchMember(value domain.BatchMember) {
	m.state.members[value.BatchID] = append(m.state.members[value.BatchID], value)
}

func (m *Mutation) RemoveBatchMember(batchID, planID string) bool {
	values := m.state.members[batchID]
	next := make([]domain.BatchMember, 0, len(values))
	removed := false
	for _, value := range values {
		if value.PlanID == planID {
			removed = true
			continue
		}
		next = append(next, value)
	}
	m.state.members[batchID] = next
	return removed
}

// StagesForBatch returns shared stage facts in sequence order.
func (m *Mutation) StagesForBatch(batchID string) []domain.BatchStage {
	values := append([]domain.BatchStage(nil), m.state.stages[batchID]...)
	sort.Slice(values, func(i, j int) bool {
		return values[i].Sequence < values[j].Sequence
	})
	return values
}

func (m *Mutation) GetBatchStage(batchID string, stageID int) (domain.BatchStage, bool) {
	for _, value := range m.state.stages[batchID] {
		if value.ID == stageID {
			return value, true
		}
	}
	return domain.BatchStage{}, false
}

// NextStageID returns a deterministic within-batch numeric id. All mutations
// run under the repository mutex, so callers receive unique ids.
func (m *Mutation) NextStageID(batchID string) int {
	next := m.state.stageSeq[batchID] + 1
	m.state.stageSeq[batchID] = next
	return next
}

func (m *Mutation) AppendBatchStage(value domain.BatchStage) {
	m.state.stages[value.BatchID] = append(m.state.stages[value.BatchID], value)
}
