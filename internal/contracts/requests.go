package contracts

type RegisterSpecimenRequest struct {
	CatalogNumber string `json:"catalog_number"`
	FieldLabel    string `json:"field_label"`
	TaxonHint     string `json:"taxon_hint"`
	MatrixKind    string `json:"matrix_kind"`
	JacketNotes   string `json:"jacket_notes"`
}

type CreatePlanRequest struct {
	Objective      string `json:"objective"`
	Method         string `json:"method"`
	Adhesive       string `json:"adhesive"`
	TargetExposure int    `json:"target_exposure"`
}

type ApprovalRequest struct {
	ApprovedBy string `json:"approved_by"`
}

type ProgressRequest struct {
	Stage        string `json:"stage"`
	Percent      int    `json:"percent"`
	Note         string `json:"note"`
	RecordedBy   string `json:"recorded_by"`
	BatchStageID int    `json:"batch_stage_id,omitempty"`
}

type ReleaseRequest struct {
	Purpose    string `json:"purpose"`
	Recipient  string `json:"recipient"`
	ApprovedBy string `json:"approved_by"`
}

// BatchMemberSpec creates one specimen plus its independent draft plan as a
// batch member in the same atomic write.
type BatchMemberSpec struct {
	CatalogNumber  string `json:"catalog_number"`
	FieldLabel     string `json:"field_label"`
	TaxonHint      string `json:"taxon_hint"`
	MatrixKind     string `json:"matrix_kind"`
	JacketNotes    string `json:"jacket_notes"`
	Objective      string `json:"objective"`
	Method         string `json:"method"`
	Adhesive       string `json:"adhesive"`
	TargetExposure int    `json:"target_exposure"`
}

type CreateBatchRequest struct {
	Site    string            `json:"site"`
	Purpose string            `json:"purpose"`
	Owner   string            `json:"owner"`
	Members []BatchMemberSpec `json:"members"`
}

type BatchApprovalRequest struct {
	ApprovedBy string `json:"approved_by"`
}

// BatchStageEntrySpec records one member's independent percentage against a
// shared stage fact. A stage fact may be written with no entries.
type BatchStageEntrySpec struct {
	PlanID  string `json:"plan_id"`
	Percent int    `json:"percent"`
	Note    string `json:"note"`
}

type RecordBatchStageRequest struct {
	Stage      string                `json:"stage"`
	Note       string                `json:"note"`
	RecordedBy string                `json:"recorded_by"`
	Entries    []BatchStageEntrySpec `json:"entries"`
}

type SplitBatchRequest struct {
	PlanIDs []string `json:"plan_ids"`
}
