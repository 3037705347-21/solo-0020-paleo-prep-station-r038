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
	w.Header().Set("Location", "/batches/"+value.ID)
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

func (api *API) logBatchProgress(w http.ResponseWriter, r *http.Request) {
	var request contracts.BatchProgressRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.LogBatchProgress(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
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
	w.Header().Set("Location", "/batches/"+value.ID)
	writeJSON(w, http.StatusCreated, value)
}

func (api *API) cancelBatch(w http.ResponseWriter, r *http.Request) {
	var request contracts.CancelBatchRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.CancelBatch(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) cancelPlan(w http.ResponseWriter, r *http.Request) {
	var request contracts.CancelPlanRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.CancelPlan(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
