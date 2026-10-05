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
	Stage      string `json:"stage"`
	Percent    int    `json:"percent"`
	Note       string `json:"note"`
	RecordedBy string `json:"recorded_by"`
}

type ReleaseRequest struct {
	Purpose    string `json:"purpose"`
	Recipient  string `json:"recipient"`
	ApprovedBy string `json:"approved_by"`
}

type CreateBatchRequest struct {
	SiteLabel  string   `json:"site_label"`
	Supervisor string   `json:"supervisor"`
	PlanIDs    []string `json:"plan_ids"`
}

// BatchProgressItem 是一份标本在共享阶段中的独立进度。
type BatchProgressItem struct {
	PlanID  string `json:"plan_id"`
	Percent int    `json:"percent"`
}

type BatchProgressRequest struct {
	Stage      string              `json:"stage"`
	Note       string              `json:"note"`
	RecordedBy string              `json:"recorded_by"`
	Items      []BatchProgressItem `json:"items"`
}

type SplitBatchRequest struct {
	PlanIDs   []string `json:"plan_ids"`
	SiteLabel string   `json:"site_label"`
}

type CancelBatchRequest struct {
	CancelledBy string `json:"cancelled_by"`
}

type CancelPlanRequest struct {
	CancelledBy string `json:"cancelled_by"`
}
