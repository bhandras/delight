package handlers

import (
	"context"
	"encoding/base64"
	"log"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/models"
)

// ArtifactUpdate applies an artifact header/body update and returns an ACK plus
// any resulting update events that should be broadcast.
func ArtifactUpdate(ctx context.Context, deps Deps, auth AuthContext, req protocolwire.ArtifactUpdateRequest) EventResult {
	if req.ArtifactID == "" {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid parameters"}, nil)
	}

	headerPresent := req.Header != nil
	bodyPresent := req.Body != nil
	if !headerPresent && !bodyPresent {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "No updates provided"}, nil)
	}

	headerData := ""
	bodyData := ""
	headerExpected := int64(0)
	bodyExpected := int64(0)
	if req.Header != nil {
		headerData = req.Header.Data
		headerExpected = req.Header.ExpectedVersion
	}
	if req.Body != nil {
		bodyData = req.Body.Data
		bodyExpected = req.Body.ExpectedVersion
	}

	if headerPresent && headerData == "" {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid header parameters"}, nil)
	}
	if bodyPresent && bodyData == "" {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid body parameters"}, nil)
	}

	current, err := deps.Artifacts().GetArtifactByIDAndAccount(ctx, models.GetArtifactByIDAndAccountParams{
		ID:        req.ArtifactID,
		AccountID: auth.UserID(),
	})
	if err != nil {
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Artifact not found"}, nil)
	}

	headerMismatch := headerPresent && current.HeaderVersion != headerExpected
	bodyMismatch := bodyPresent && current.BodyVersion != bodyExpected
	if headerMismatch || bodyMismatch {
		resp := protocolwire.ArtifactUpdateMismatchAck{Result: "version-mismatch"}
		if headerMismatch {
			resp.Header = &protocolwire.ArtifactPartMismatch{
				CurrentVersion: current.HeaderVersion,
				CurrentData:    base64.StdEncoding.EncodeToString(current.Header),
			}
		}
		if bodyMismatch {
			resp.Body = &protocolwire.ArtifactPartMismatch{
				CurrentVersion: current.BodyVersion,
				CurrentData:    base64.StdEncoding.EncodeToString(current.Body),
			}
		}
		return NewEventResult(resp, nil)
	}

	var headerBytes []byte
	var bodyBytes []byte
	if headerPresent {
		headerBytes, err = base64.StdEncoding.DecodeString(headerData)
		if err != nil {
			return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid header encoding"}, nil)
		}
	}
	if bodyPresent {
		bodyBytes, err = base64.StdEncoding.DecodeString(bodyData)
		if err != nil {
			return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Invalid body encoding"}, nil)
		}
	}

	rows, err := deps.Artifacts().UpdateArtifact(ctx, models.UpdateArtifactParams{
		Header:          headerBytes,
		Column2:         boolToInt64(headerPresent),
		HeaderVersion:   headerExpected + 1,
		Body:            bodyBytes,
		Column5:         boolToInt64(bodyPresent),
		BodyVersion:     bodyExpected + 1,
		ID:              req.ArtifactID,
		AccountID:       auth.UserID(),
		Column9:         boolToInt64(headerPresent),
		HeaderVersion_2: headerExpected,
		Column11:        boolToInt64(bodyPresent),
		BodyVersion_2:   bodyExpected,
	})
	if err != nil || rows == 0 {
		current, err := deps.Artifacts().GetArtifactByIDAndAccount(ctx, models.GetArtifactByIDAndAccountParams{
			ID:        req.ArtifactID,
			AccountID: auth.UserID(),
		})
		if err == nil {
			resp := protocolwire.ArtifactUpdateMismatchAck{Result: "version-mismatch"}
			if headerPresent {
				resp.Header = &protocolwire.ArtifactPartMismatch{
					CurrentVersion: current.HeaderVersion,
					CurrentData:    base64.StdEncoding.EncodeToString(current.Header),
				}
			}
			if bodyPresent {
				resp.Body = &protocolwire.ArtifactPartMismatch{
					CurrentVersion: current.BodyVersion,
					CurrentData:    base64.StdEncoding.EncodeToString(current.Body),
				}
			}
			return NewEventResult(resp, nil)
		}
		return NewEventResult(protocolwire.ArtifactAck{Result: "error", Message: "Internal error"}, nil)
	}

	var headerUpdate *protocolwire.VersionedString
	var bodyUpdate *protocolwire.VersionedString
	if headerPresent {
		headerUpdate = &protocolwire.VersionedString{Value: headerData, Version: headerExpected + 1}
	}
	if bodyPresent {
		bodyUpdate = &protocolwire.VersionedString{Value: bodyData, Version: bodyExpected + 1}
	}

	ack := protocolwire.ArtifactUpdateSuccessAck{Result: "success"}
	if headerUpdate != nil {
		ack.Header = &protocolwire.ArtifactUpdateSuccessPart{Version: headerUpdate.Version, Data: headerData}
	}
	if bodyUpdate != nil {
		ack.Body = &protocolwire.ArtifactUpdateSuccessPart{Version: bodyUpdate.Version, Data: bodyData}
	}

	userSeq, err := deps.Accounts().UpdateAccountSeq(ctx, auth.UserID())
	if err != nil {
		log.Printf("Failed to allocate user seq: %v", err)
		return NewEventResult(ack, nil)
	}

	event := protocolwire.UpdateEvent{
		ID:        deps.NewID(),
		Seq:       userSeq,
		CreatedAt: deps.Now().UnixMilli(),
		Body: protocolwire.UpdateBodyUpdateArtifact{
			T:          "update-artifact",
			ArtifactID: req.ArtifactID,
			Header:     headerUpdate,
			Body:       bodyUpdate,
		},
	}

	return NewEventResult(ack, []UpdateInstruction{newUserUpdate(auth.UserID(), event)})
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
