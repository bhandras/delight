package handlers

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/bhandras/delight/server/internal/api/middleware"
	"github.com/bhandras/delight/server/internal/models"
	"github.com/bhandras/delight/server/internal/websocket"
	"github.com/bhandras/delight/server/pkg/types"
	protocolwire "github.com/bhandras/delight/shared/wire"
	"github.com/gin-gonic/gin"
)

type ArtifactHandler struct {
	db      *sql.DB
	queries *models.Queries
	updates *websocket.SocketIOServer
}

func NewArtifactHandler(db *sql.DB, updates *websocket.SocketIOServer) *ArtifactHandler {
	return &ArtifactHandler{
		db:      db,
		queries: models.New(db),
		updates: updates,
	}
}

type ArtifactInfoResponse struct {
	ID                string `json:"id"`
	Header            string `json:"header"`
	HeaderVersion     int64  `json:"headerVersion"`
	DataEncryptionKey string `json:"dataEncryptionKey"`
	Seq               int64  `json:"seq"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
}

type ArtifactResponse struct {
	ID                string `json:"id"`
	Header            string `json:"header"`
	HeaderVersion     int64  `json:"headerVersion"`
	Body              string `json:"body"`
	BodyVersion       int64  `json:"bodyVersion"`
	DataEncryptionKey string `json:"dataEncryptionKey"`
	Seq               int64  `json:"seq"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
}

type CreateArtifactRequest struct {
	ID                string `json:"id" binding:"required"`
	Header            string `json:"header" binding:"required"`
	Body              string `json:"body" binding:"required"`
	DataEncryptionKey string `json:"dataEncryptionKey" binding:"required"`
}

type UpdateArtifactRequest struct {
	Header                *string `json:"header"`
	ExpectedHeaderVersion *int64  `json:"expectedHeaderVersion"`
	Body                  *string `json:"body"`
	ExpectedBodyVersion   *int64  `json:"expectedBodyVersion"`
}

func (h *ArtifactHandler) ListArtifacts(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	artifacts, err := h.queries.ListArtifactsByAccount(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get artifacts"})
		return
	}

	result := make([]ArtifactInfoResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, ArtifactInfoResponse{
			ID:                artifact.ID,
			Header:            base64.StdEncoding.EncodeToString(artifact.Header),
			HeaderVersion:     artifact.HeaderVersion,
			DataEncryptionKey: base64.StdEncoding.EncodeToString(artifact.DataEncryptionKey),
			Seq:               artifact.Seq,
			CreatedAt:         artifact.CreatedAt.UnixMilli(),
			UpdatedAt:         artifact.UpdatedAt.UnixMilli(),
		})
	}

	c.JSON(http.StatusOK, result)
}

func (h *ArtifactHandler) GetArtifact(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	artifactID := c.Param("id")

	artifact, err := h.queries.GetArtifactByIDAndAccount(c.Request.Context(), models.GetArtifactByIDAndAccountParams{
		ID:        artifactID,
		AccountID: userID,
	})
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Artifact not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get artifact"})
		return
	}

	c.JSON(http.StatusOK, ArtifactResponse{
		ID:                artifact.ID,
		Header:            base64.StdEncoding.EncodeToString(artifact.Header),
		HeaderVersion:     artifact.HeaderVersion,
		Body:              base64.StdEncoding.EncodeToString(artifact.Body),
		BodyVersion:       artifact.BodyVersion,
		DataEncryptionKey: base64.StdEncoding.EncodeToString(artifact.DataEncryptionKey),
		Seq:               artifact.Seq,
		CreatedAt:         artifact.CreatedAt.UnixMilli(),
		UpdatedAt:         artifact.UpdatedAt.UnixMilli(),
	})
}

func (h *ArtifactHandler) CreateArtifact(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)

	var req CreateArtifactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	existing, err := h.queries.GetArtifactByID(c.Request.Context(), req.ID)
	if err == nil {
		if existing.AccountID != userID {
			c.JSON(http.StatusConflict, gin.H{"error": "Artifact with this ID already exists for another account"})
			return
		}
		c.JSON(http.StatusOK, ArtifactResponse{
			ID:                existing.ID,
			Header:            base64.StdEncoding.EncodeToString(existing.Header),
			HeaderVersion:     existing.HeaderVersion,
			Body:              base64.StdEncoding.EncodeToString(existing.Body),
			BodyVersion:       existing.BodyVersion,
			DataEncryptionKey: base64.StdEncoding.EncodeToString(existing.DataEncryptionKey),
			Seq:               existing.Seq,
			CreatedAt:         existing.CreatedAt.UnixMilli(),
			UpdatedAt:         existing.UpdatedAt.UnixMilli(),
		})
		return
	} else if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create artifact"})
		return
	}

	header, err := base64.StdEncoding.DecodeString(req.Header)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid header encoding"})
		return
	}
	body, err := base64.StdEncoding.DecodeString(req.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid body encoding"})
		return
	}
	dataKey, err := base64.StdEncoding.DecodeString(req.DataEncryptionKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid dataEncryptionKey encoding"})
		return
	}
	if len(dataKey) == 0 || len(dataKey) > maxWrappedDataKeyBytes {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid dataEncryptionKey size"})
		return
	}

	if err := h.queries.CreateArtifact(c.Request.Context(), models.CreateArtifactParams{
		ID:                req.ID,
		AccountID:         userID,
		Header:            header,
		HeaderVersion:     1,
		Body:              body,
		BodyVersion:       1,
		DataEncryptionKey: dataKey,
		Seq:               0,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create artifact"})
		return
	}

	artifact, err := h.queries.GetArtifactByIDAndAccount(c.Request.Context(), models.GetArtifactByIDAndAccountParams{
		ID:        req.ID,
		AccountID: userID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch artifact"})
		return
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err == nil {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyNewArtifact{
					T:                 "new-artifact",
					ArtifactID:        artifact.ID,
					Seq:               artifact.Seq,
					Header:            base64.StdEncoding.EncodeToString(artifact.Header),
					HeaderVersion:     artifact.HeaderVersion,
					Body:              base64.StdEncoding.EncodeToString(artifact.Body),
					BodyVersion:       artifact.BodyVersion,
					DataEncryptionKey: base64.StdEncoding.EncodeToString(artifact.DataEncryptionKey),
					CreatedAt:         artifact.CreatedAt.UnixMilli(),
					UpdatedAt:         artifact.UpdatedAt.UnixMilli(),
				},
			})
		}
	}

	c.JSON(http.StatusOK, ArtifactResponse{
		ID:                artifact.ID,
		Header:            base64.StdEncoding.EncodeToString(artifact.Header),
		HeaderVersion:     artifact.HeaderVersion,
		Body:              base64.StdEncoding.EncodeToString(artifact.Body),
		BodyVersion:       artifact.BodyVersion,
		DataEncryptionKey: base64.StdEncoding.EncodeToString(artifact.DataEncryptionKey),
		Seq:               artifact.Seq,
		CreatedAt:         artifact.CreatedAt.UnixMilli(),
		UpdatedAt:         artifact.UpdatedAt.UnixMilli(),
	})
}

func (h *ArtifactHandler) UpdateArtifact(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	artifactID := c.Param("id")

	var req UpdateArtifactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: err.Error()})
		return
	}

	if req.Header == nil && req.Body == nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "no updates provided"})
		return
	}

	if req.Header != nil && req.ExpectedHeaderVersion == nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "expectedHeaderVersion is required"})
		return
	}
	if req.Body != nil && req.ExpectedBodyVersion == nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "expectedBodyVersion is required"})
		return
	}

	current, err := h.queries.GetArtifactByIDAndAccount(c.Request.Context(), models.GetArtifactByIDAndAccountParams{
		ID:        artifactID,
		AccountID: userID,
	})
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Artifact not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update artifact"})
		return
	}

	headerMismatch := req.Header != nil && current.HeaderVersion != *req.ExpectedHeaderVersion
	bodyMismatch := req.Body != nil && current.BodyVersion != *req.ExpectedBodyVersion
	if headerMismatch || bodyMismatch {
		resp := gin.H{
			"success": false,
			"error":   "version-mismatch",
		}
		if headerMismatch {
			resp["currentHeaderVersion"] = current.HeaderVersion
			resp["currentHeader"] = base64.StdEncoding.EncodeToString(current.Header)
		}
		if bodyMismatch {
			resp["currentBodyVersion"] = current.BodyVersion
			resp["currentBody"] = base64.StdEncoding.EncodeToString(current.Body)
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	headerPresent := req.Header != nil
	bodyPresent := req.Body != nil
	var headerData []byte
	var bodyData []byte
	var errDecode error
	if headerPresent {
		headerData, errDecode = base64.StdEncoding.DecodeString(*req.Header)
		if errDecode != nil {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid header encoding"})
			return
		}
	}
	if bodyPresent {
		bodyData, errDecode = base64.StdEncoding.DecodeString(*req.Body)
		if errDecode != nil {
			c.JSON(http.StatusBadRequest, types.ErrorResponse{Error: "invalid body encoding"})
			return
		}
	}

	expectedHeaderVersion := int64(0)
	if req.ExpectedHeaderVersion != nil {
		expectedHeaderVersion = *req.ExpectedHeaderVersion
	}
	expectedBodyVersion := int64(0)
	if req.ExpectedBodyVersion != nil {
		expectedBodyVersion = *req.ExpectedBodyVersion
	}

	updatedRows, err := h.queries.UpdateArtifact(c.Request.Context(), models.UpdateArtifactParams{
		Header:          headerData,
		Column2:         boolToInt64(headerPresent),
		HeaderVersion:   expectedHeaderVersion + 1,
		Body:            bodyData,
		Column5:         boolToInt64(bodyPresent),
		BodyVersion:     expectedBodyVersion + 1,
		ID:              artifactID,
		AccountID:       userID,
		Column9:         boolToInt64(headerPresent),
		HeaderVersion_2: expectedHeaderVersion,
		Column11:        boolToInt64(bodyPresent),
		BodyVersion_2:   expectedBodyVersion,
	})
	if err != nil || updatedRows == 0 {
		current, err := h.queries.GetArtifactByIDAndAccount(c.Request.Context(), models.GetArtifactByIDAndAccountParams{
			ID:        artifactID,
			AccountID: userID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update artifact"})
			return
		}
		resp := gin.H{
			"success": false,
			"error":   "version-mismatch",
		}
		if headerPresent {
			resp["currentHeaderVersion"] = current.HeaderVersion
			resp["currentHeader"] = base64.StdEncoding.EncodeToString(current.Header)
		}
		if bodyPresent {
			resp["currentBodyVersion"] = current.BodyVersion
			resp["currentBody"] = base64.StdEncoding.EncodeToString(current.Body)
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	var headerUpdate *protocolwire.VersionedString
	var bodyUpdate *protocolwire.VersionedString
	if headerPresent {
		headerUpdate = &protocolwire.VersionedString{
			Value:   *req.Header,
			Version: expectedHeaderVersion + 1,
		}
	}
	if bodyPresent {
		bodyUpdate = &protocolwire.VersionedString{
			Value:   *req.Body,
			Version: expectedBodyVersion + 1,
		}
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err == nil {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateArtifact{
					T:          "update-artifact",
					ArtifactID: artifactID,
					Header:     headerUpdate,
					Body:       bodyUpdate,
				},
			})
		}
	}

	resp := gin.H{"success": true}
	if headerUpdate != nil {
		resp["headerVersion"] = headerUpdate.Version
	}
	if bodyUpdate != nil {
		resp["bodyVersion"] = bodyUpdate.Version
	}
	c.JSON(http.StatusOK, resp)
}

func (h *ArtifactHandler) DeleteArtifact(c *gin.Context) {
	userID, _ := middleware.GetUserID(c)
	artifactID := c.Param("id")

	_, err := h.queries.GetArtifactByIDAndAccount(c.Request.Context(), models.GetArtifactByIDAndAccountParams{
		ID:        artifactID,
		AccountID: userID,
	})
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Artifact not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete artifact"})
		return
	}

	if err := h.queries.DeleteArtifact(c.Request.Context(), models.DeleteArtifactParams{
		ID:        artifactID,
		AccountID: userID,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete artifact"})
		return
	}

	if h.updates != nil {
		userSeq, err := h.queries.UpdateAccountSeq(c.Request.Context(), userID)
		if err == nil {
			h.updates.EmitUpdateToUser(userID, protocolwire.UpdateEvent{
				ID:        types.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyDeleteArtifact{
					T:          "delete-artifact",
					ArtifactID: artifactID,
				},
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
