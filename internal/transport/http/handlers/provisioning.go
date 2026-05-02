package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	provapp "github.com/celinet/sentinel-acs/internal/application/provisioning"
	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	"github.com/celinet/sentinel-acs/internal/infrastructure/postgres"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
)

// ProvisioningHandler — endpoints de aplicar profile + listar jobs/batches.
type ProvisioningHandler struct {
	Service *provapp.Service
	Jobs    *postgres.JobRepo
	Batches *postgres.BatchRepo
	Devices domain.DeviceRepository
}

// PreviewToDevice POST /devices/{id}/templates/{profileID}/preview
// Devolve JSON com os parâmetros já renderizados para confirmação na UI.
func (h *ProvisioningHandler) PreviewToDevice(w http.ResponseWriter, r *http.Request) {
	deviceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "device id inválido", http.StatusBadRequest)
		return
	}
	profileID, err := uuid.Parse(chi.URLParam(r, "profileID"))
	if err != nil {
		http.Error(w, "profile id inválido", http.StatusBadRequest)
		return
	}
	resolved, err := h.Service.PreviewToDevice(r.Context(), profileID, deviceID)
	if err != nil {
		logger.FromContext(r.Context()).Error("preview", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Mascaramos secret no preview — UI mostra "●●●●●".
	out := make([]map[string]any, 0, len(resolved))
	for _, p := range resolved {
		v := any(p.Value)
		if p.IsSecret {
			v = "●●●●●"
		}
		out = append(out, map[string]any{
			"canonical_key": p.CanonicalKey,
			"tr_path":       p.TRPath,
			"value":         v,
			"data_type":     string(p.DataType),
			"is_secret":     p.IsSecret,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// ApplyToDevice POST /devices/{id}/templates/{profileID}/apply
// Cria 1 job e redireciona para o detalhe do device com info do task.
func (h *ProvisioningHandler) ApplyToDevice(w http.ResponseWriter, r *http.Request) {
	deviceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "device id inválido", http.StatusBadRequest)
		return
	}
	profileID, err := uuid.Parse(chi.URLParam(r, "profileID"))
	if err != nil {
		http.Error(w, "profile id inválido", http.StatusBadRequest)
		return
	}
	user, _ := mw.UserFromContext(r.Context())
	var requestedBy *uuid.UUID
	if user != nil {
		requestedBy = &user.ID
	}
	job, err := h.Service.ApplyToDevice(r.Context(), profileID, deviceID, requestedBy)
	if err != nil {
		logger.FromContext(r.Context()).Error("apply to device", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/provisioning/jobs/"+job.ID.String(), http.StatusSeeOther)
}

// ApplyBulk POST /templates/{id}/apply-bulk
// Form: device_ids[] múltiplos + filter_summary
func (h *ProvisioningHandler) ApplyBulk(w http.ResponseWriter, r *http.Request) {
	profileID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "profile id inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	user, _ := mw.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "auth required", http.StatusUnauthorized)
		return
	}
	rawIDs := r.PostForm["device_ids[]"]
	deviceIDs := make([]uuid.UUID, 0, len(rawIDs))
	for _, s := range rawIDs {
		if id, err := uuid.Parse(s); err == nil {
			deviceIDs = append(deviceIDs, id)
		}
	}
	if len(deviceIDs) == 0 {
		http.Error(w, "ao menos 1 device é obrigatório", http.StatusBadRequest)
		return
	}
	res, err := h.Service.ApplyBulk(r.Context(), provapp.BulkRequest{
		ProfileID:     profileID,
		DeviceIDs:     deviceIDs,
		RequestedBy:   user.ID,
		FilterSummary: r.PostForm.Get("filter_summary"),
	})
	if err != nil {
		logger.FromContext(r.Context()).Error("apply bulk", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/provisioning/batches/"+res.BatchID.String(), http.StatusSeeOther)
}

// JobDetail GET /provisioning/jobs/{id}
func (h *ProvisioningHandler) JobDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	job, err := h.Jobs.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "job não encontrado", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":              job.ID,
		"device_id":       job.DeviceID,
		"profile_id":      job.ProfileID,
		"status":          string(job.Status),
		"genieacs_task":   job.GenieACSTaskID,
		"error":           job.ErrorMessage,
		"retry_count":     job.RetryCount,
		"scheduled_at":    job.ScheduledAt,
		"started_at":      job.StartedAt,
		"finished_at":     job.FinishedAt,
		"created_at":      job.CreatedAt,
	})
}

// BatchDetail GET /provisioning/batches/{id}
func (h *ProvisioningHandler) BatchDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	batch, err := h.Batches.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "batch não encontrado", http.StatusNotFound)
		return
	}
	jobs, _ := h.Jobs.List(r.Context(), postgres.JobListFilter{BatchID: &id, Limit: 200})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"batch": map[string]any{
			"id":              batch.ID,
			"profile_id":      batch.ProfileID,
			"profile_version": batch.ProfileVersion,
			"requested_by":    batch.RequestedBy,
			"filter_summary":  batch.FilterSummary,
			"total":           batch.TotalDevices,
			"queued":          batch.Queued,
			"done":            batch.Done,
			"failed":          batch.Failed,
			"cancelled":       batch.Cancelled,
			"status":          string(batch.Status),
			"approved_at":     batch.ApprovedAt,
			"created_at":      batch.CreatedAt,
			"finished_at":     batch.FinishedAt,
		},
		"jobs": jobs,
	})
}

// ApproveBatch POST /provisioning/batches/{id}/approve
func (h *ProvisioningHandler) ApproveBatch(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	user, _ := mw.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "auth required", http.StatusUnauthorized)
		return
	}
	if err := h.Batches.Approve(r.Context(), id, user.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/provisioning/batches/"+id.String(), http.StatusSeeOther)
}

// JobsList GET /provisioning/jobs?status=&limit=
func (h *ProvisioningHandler) JobsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	jobs, err := h.Jobs.List(r.Context(), postgres.JobListFilter{
		Status: q.Get("status"),
		Limit:  limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Não decodificamos payload para não vazar valores secret no JSON.
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, map[string]any{
			"id":           j.ID,
			"device_id":    j.DeviceID,
			"profile_id":   j.ProfileID,
			"batch_id":     j.BatchID,
			"status":       string(j.Status),
			"created_at":   j.CreatedAt,
			"finished_at":  j.FinishedAt,
			"retry_count":  j.RetryCount,
			"error":        j.ErrorMessage,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// BatchInProgress — estado intermediário usado por status badge.
// Helper exposto para outros handlers se necessário.
func BatchInProgress(s prov.BatchStatus) bool {
	return s == prov.BatchQueued || s == prov.BatchRunning
}
