package control

import (
	"context"
	"errors"
	"net/http"

	"github.com/fallingnight/akv/internal/identity"
	"github.com/fallingnight/akv/internal/observation"
)

const observationIdempotencyHeader = "Idempotency-Key"

type WebObservationManager interface {
	Record(context.Context, identity.User, string, observation.EventType, string, string) (observation.Event, bool, error)
	Summarize(context.Context, identity.User, string) (observation.Summary, error)
}

func (runtime *WebRuntime) authorizationPilotMetrics(response http.ResponseWriter, request *http.Request) {
	actor, _, ok := runtime.authenticate(response, request)
	if !ok {
		return
	}
	summary, err := runtime.Observations.Summarize(request.Context(), actor, request.PathValue("request_id"))
	if err != nil {
		writeObservationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, summary)
}

func (runtime *WebRuntime) recordManualHandoff(response http.ResponseWriter, request *http.Request) {
	runtime.recordObservation(response, request, observation.EventManualHandoff, "")
}

func (runtime *WebRuntime) recordApprovalFollowup(response http.ResponseWriter, request *http.Request) {
	runtime.recordObservation(response, request, observation.EventApprovalFollowup, "")
}

func (runtime *WebRuntime) startOperationReview(response http.ResponseWriter, request *http.Request) {
	runtime.recordObservation(response, request, observation.EventReviewStarted, "")
}

func (runtime *WebRuntime) completeOperationReview(response http.ResponseWriter, request *http.Request) {
	runtime.recordObservation(response, request, observation.EventReviewCompleted, request.PathValue("review_id"))
}

func (runtime *WebRuntime) recordObservation(
	response http.ResponseWriter,
	request *http.Request,
	eventType observation.EventType,
	reviewSessionID string,
) {
	actor, ok := runtime.authorizeMutation(response, request)
	if !ok {
		return
	}
	var input struct{}
	if !decodeStrict(response, request, &input) {
		return
	}
	event, created, err := runtime.Observations.Record(
		request.Context(), actor, request.PathValue("request_id"), eventType,
		reviewSessionID, request.Header.Get(observationIdempotencyHeader),
	)
	if err != nil {
		writeObservationError(response, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(response, status, event)
}

func writeObservationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, observation.ErrInvalidInput):
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "INVALID_OBSERVATION"})
	case errors.Is(err, observation.ErrNotFound):
		writeJSON(response, http.StatusNotFound, map[string]string{"error": "NOT_FOUND"})
	case errors.Is(err, observation.ErrUnavailable), errors.Is(err, observation.ErrConflict):
		writeJSON(response, http.StatusConflict, map[string]string{"error": "OBSERVATION_REJECTED"})
	default:
		writeJSON(response, http.StatusInternalServerError, map[string]string{"error": "INTERNAL"})
	}
}
