package httpapi

import (
	"net/http"

	"example.com/paleo-prep-station/internal/contracts"
)

func (api *API) createBatch(w http.ResponseWriter, r *http.Request) {
	var request contracts.CreateBatchRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.CreateBatch(r.Context(), request)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/prep-batches/"+value.Batch.ID)
	writeJSON(w, http.StatusCreated, value)
}

func (api *API) listBatches(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListBatches(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}

func (api *API) getBatch(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.GetBatch(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) approveBatch(w http.ResponseWriter, r *http.Request) {
	var request contracts.BatchApprovalRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.ApproveBatch(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) recordBatchStage(w http.ResponseWriter, r *http.Request) {
	var request contracts.RecordBatchStageRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.RecordBatchStage(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (api *API) closeBatch(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.CloseBatch(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) cancelBatch(w http.ResponseWriter, r *http.Request) {
	value, err := api.service.CancelBatch(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) splitBatch(w http.ResponseWriter, r *http.Request) {
	var request contracts.SplitBatchRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.SplitBatch(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/prep-batches/"+value.Batch.ID)
	writeJSON(w, http.StatusCreated, value)
}
