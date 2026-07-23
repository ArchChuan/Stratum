package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/gin-gonic/gin"
)

type UpdateSystemAssistantModelRequest struct {
	LLMModel string `json:"llmModel"`
}

type SystemAssistantSettingsResponse struct {
	AgentID  string `json:"agentId"`
	LLMModel string `json:"llmModel"`
	Ready    bool   `json:"ready"`
}

func (h *AgentHandler) GetSettings(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	settings, err := h.svc.GetSystemAssistantSettings(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, settingsResponse(settings.AgentID, settings.Model, settings.Ready))
}

func (h *AgentHandler) UpdateModel(c *gin.Context) {
	if _, ok := tenantIDFromCtx(c); !ok {
		respondMissingTenant(c)
		return
	}
	var req UpdateSystemAssistantModelRequest
	if err := decodeClosedJSON(c, &req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	settings, err := h.svc.UpdateSystemAssistantModel(c.Request.Context(), req.LLMModel)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, settingsResponse(settings.AgentID, settings.Model, settings.Ready))
}

func settingsResponse(agentID, model string, ready bool) SystemAssistantSettingsResponse {
	return SystemAssistantSettingsResponse{AgentID: agentID, LLMModel: model, Ready: ready}
}

func decodeClosedJSON(c *gin.Context, dst any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode request body: multiple JSON values")
		}
		return fmt.Errorf("decode request body: %w", err)
	}
	return nil
}
