package websocket

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	protocolwire "github.com/bhandras/delight/protocol/wire"
	"github.com/bhandras/delight/server/internal/crypto"
	"github.com/bhandras/delight/server/internal/models"
	pkgtypes "github.com/bhandras/delight/server/pkg/types"
	"github.com/gin-gonic/gin"
	socket "github.com/zishang520/socket.io/servers/socket/v3"
	sockettypes "github.com/zishang520/socket.io/v3/pkg/types"
)

// SocketIOServer wraps Socket.IO server for Delight
type SocketIOServer struct {
	db         *sql.DB
	jwtManager *crypto.JWTManager
	server     *socket.Server
	socketData sync.Map // Maps socket ID to user data
	rpcMu      sync.Mutex
	rpcMethods map[string]map[string]*socket.Socket // userID -> method -> socket
}

// NewSocketIOServer creates a new Socket.IO v4 server
func NewSocketIOServer(db *sql.DB, jwtManager *crypto.JWTManager) *SocketIOServer {
	// Create default server options
	opts := socket.DefaultServerOptions()

	// Configure CORS
	opts.SetCors(&sockettypes.Cors{
		Origin:      "*",
		Credentials: true,
	})

	// Set ping timeout and interval to match TypeScript server
	opts.SetPingTimeout(45 * time.Second)
	opts.SetPingInterval(15 * time.Second)

	// Set the path to match what mobile app expects (same as TypeScript server)
	opts.SetPath("/v1/updates")

	// Create Socket.IO server with options
	server := socket.NewServer(nil, opts)

	s := &SocketIOServer{
		db:         db,
		jwtManager: jwtManager,
		server:     server,
		socketData: sync.Map{},
		rpcMethods: make(map[string]map[string]*socket.Socket),
	}

	// Set up event handlers
	s.setupHandlers()

	return s
}

// SocketData stores connection metadata for each socket
type SocketData struct {
	UserID     string
	ClientType string // "session-scoped", "user-scoped", or "machine-scoped"
	SessionID  string
	MachineID  string
	Socket     *socket.Socket // Reference to the socket for emitting
}

// setupHandlers configures Socket.IO event handlers
func (s *SocketIOServer) setupHandlers() {
	queries := models.New(s.db)

	// Connection handler
	s.server.On("connection", func(clients ...any) {
		client := clients[0].(*socket.Socket)

		log.Printf("🔌 Socket.IO connection attempt! Socket ID: %s", client.Id())

		// Get handshake data
		handshake := client.Handshake()
		log.Printf("  Headers: %+v", handshake.Headers)
		log.Printf("  Query: %+v", handshake.Query)
		log.Printf("  Auth: %+v", handshake.Auth)

		// Extract auth data from handshake
		// handshake.Auth is map[string]any
		authMap := handshake.Auth
		if authMap == nil || len(authMap) == 0 {
			log.Printf("❌ No auth data provided (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Missing authentication data"})
			client.Disconnect(true)
			return
		}

		var auth protocolwire.SocketAuthPayload
		if err := decodeAny(authMap, &auth); err != nil {
			log.Printf("❌ Invalid auth data provided (socket %s): %v", client.Id(), err)
			client.Emit("error", map[string]string{"message": "Invalid authentication data"})
			client.Disconnect(true)
			return
		}

		// Extract token
		token := auth.Token
		if token == "" {
			log.Printf("❌ No token provided (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Missing authentication token"})
			client.Disconnect(true)
			return
		}

		// Extract client type and optional IDs
		clientType := auth.ClientType
		if clientType == "" {
			clientType = "user-scoped" // Default to user-scoped
		}
		sessionID := auth.SessionID
		machineID := auth.MachineID

		// Validate session-scoped clients have sessionId
		if clientType == "session-scoped" && sessionID == "" {
			log.Printf("❌ Session-scoped client missing sessionId (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Session ID required for session-scoped clients"})
			client.Disconnect(true)
			return
		}

		// Validate machine-scoped clients have machineId
		if clientType == "machine-scoped" && machineID == "" {
			log.Printf("❌ Machine-scoped client missing machineId (socket %s)", client.Id())
			client.Emit("error", map[string]string{"message": "Machine ID required for machine-scoped clients"})
			client.Disconnect(true)
			return
		}

		// Verify JWT token
		claims, err := s.jwtManager.VerifyToken(token)
		if err != nil {
			log.Printf("❌ Invalid token provided (socket %s): %v", client.Id(), err)
			client.Emit("error", map[string]string{"message": "Invalid authentication token"})
			client.Disconnect(true)
			return
		}

		userID := claims.Subject
		log.Printf("✅ Token verified: userID=%s, clientType=%s, sessionId=%s, machineId=%s, socketId=%s",
			userID, clientType, sessionID, machineID, client.Id())

		// Store connection metadata
		socketData := &SocketData{
			UserID:     userID,
			ClientType: clientType,
			SessionID:  sessionID,
			MachineID:  machineID,
			Socket:     client,
		}
		s.socketData.Store(client.Id(), socketData)

		log.Printf("✅ Socket.IO client ready (user: %s, clientType: %s)", userID, clientType)

		if clientType == "machine-scoped" && machineID != "" {
			now := time.Now()
			if err := queries.UpdateMachineActivity(context.Background(), models.UpdateMachineActivityParams{
				Active:       1,
				LastActiveAt: now,
				AccountID:    userID,
				ID:           machineID,
			}); err != nil {
				log.Printf("Failed to update machine activity: %v", err)
			}

			ephemeral := map[string]any{
				"type":     "machine-activity",
				"id":       machineID,
				"active":   true,
				"activeAt": now.UnixMilli(),
			}
			s.emitEphemeralToUserScoped(userID, ephemeral, "")
		}

		// Message event - broadcast to session-scoped clients
		client.On("message", func(data ...any) {
			sd := s.getSocketData(client.Id())
			log.Printf("Message event from user %s (socket %s): %+v", sd.UserID, client.Id(), data)

			// Get the message data
			if len(data) == 0 {
				log.Printf("No message data received")
				return
			}

			var payload protocolwire.OutboundMessagePayload
			if err := decodeAny(data[0], &payload); err != nil {
				log.Printf("Message data decode error: %v (type=%T)", err, data[0])
				return
			}

			// Get the target session ID from the message
			targetSessionID := payload.SID
			if targetSessionID == "" {
				// If no session ID in message, use the sender's session ID (if session-scoped)
				targetSessionID = sd.SessionID
			}

			if targetSessionID == "" {
				log.Printf("No target session ID for message")
				return
			}

			// Validate session ownership
			// Use a fresh background context for DB ops; handshake contexts can be canceled after upgrade.
			ctx := context.Background()

			session, err := queries.GetSessionByID(ctx, targetSessionID)
			if err != nil {
				log.Printf("❌ Failed to load session %s: %v", targetSessionID, err)
				return
			}
			if session.AccountID != sd.UserID {
				log.Printf("❌ Session %s does not belong to user %s", targetSessionID, sd.UserID)
				return
			}

			// Persist message with new seq
			seq, err := queries.UpdateSessionSeq(ctx, targetSessionID)
			if err != nil {
				log.Printf("❌ Failed to increment session seq: %v", err)
				return
			}

			var localID sql.NullString
			if payload.LocalID != "" {
				localID = sql.NullString{String: payload.LocalID, Valid: true}
			}

			content := payload.Message
			if content == "" {
				log.Printf("❌ No message content provided")
				return
			}

			msgID := pkgtypes.NewCUID()
			contentJSON, _ := json.Marshal(map[string]any{
				"t": "encrypted",
				"c": content,
			})

			createdMsg, err := queries.CreateMessage(ctx, models.CreateMessageParams{
				ID:        msgID,
				SessionID: targetSessionID,
				LocalID:   localID,
				Seq:       seq,
				// Store an encrypted envelope so HTTP consumers (mobile app sync)
				// get a JSON object instead of a bare string.
				Content: string(contentJSON),
			})
			if err != nil {
				log.Printf("❌ Failed to persist message: %v", err)
				return
			}

			// Mark session active
			_ = queries.UpdateSessionActivity(ctx, models.UpdateSessionActivityParams{
				Active:       1,
				LastActiveAt: time.Now(),
				ID:           targetSessionID,
			})

			// Prepare app-compatible update payload
			updateID := pkgtypes.NewCUID()
			userSeq, err := queries.UpdateAccountSeq(ctx, sd.UserID)
			if err != nil {
				log.Printf("❌ Failed to allocate user seq: %v", err)
				return
			}

			var localIDValue *string
			if localID.Valid {
				v := localID.String
				localIDValue = &v
			}

			updatePayload := protocolwire.UpdateEvent{
				ID:        updateID,
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyNewMessage{
					T:   "new-message",
					SID: targetSessionID,
					Message: protocolwire.UpdateNewMessage{
						ID:      createdMsg.ID,
						Seq:     seq,
						LocalID: localIDValue,
						Content: protocolwire.EncryptedEnvelope{
							T: "encrypted",
							C: content,
						},
						CreatedAt: createdMsg.CreatedAt.UnixMilli(),
						UpdatedAt: createdMsg.UpdatedAt.UnixMilli(),
					},
				},
			}

			log.Printf("Broadcasting new-message update to session: %s content=%s", targetSessionID, content)

			s.emitUpdateToSession(sd.UserID, targetSessionID, updatePayload, string(client.Id()))
		})

		// Session alive event
		client.On("session-alive", func(data ...any) {
			sd := s.getSocketData(client.Id())
			log.Printf("Session alive from user %s (socket %s): %+v", sd.UserID, client.Id(), data)

			payload, _ := getFirstMap(data)
			sid := getString(payload, "sid")
			if sid == "" {
				return
			}

			t := getInt64(payload, "time")
			if t == 0 {
				t = time.Now().UnixMilli()
			}

			now := time.Now().UnixMilli()
			if t > now {
				t = now
			}
			if t < now-10*60*1000 {
				return
			}

			if err := queries.UpdateSessionActivity(context.Background(), models.UpdateSessionActivityParams{
				Active:       1,
				LastActiveAt: time.UnixMilli(t),
				ID:           sid,
			}); err != nil {
				log.Printf("Failed to update session activity: %v", err)
			}

			thinking := getBool(payload, "thinking")
			ephemeral := map[string]any{
				"type":     "activity",
				"id":       sid,
				"active":   true,
				"activeAt": t,
				"thinking": thinking,
			}
			s.emitEphemeralToUser(sd.UserID, ephemeral, "")
		})

		// Machine alive event
		client.On("machine-alive", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			machineID := getString(payload, "machineId")
			if machineID == "" {
				return
			}
			t := getInt64(payload, "time")
			if t == 0 {
				t = time.Now().UnixMilli()
			}

			now := time.Now().UnixMilli()
			if t > now {
				t = now
			}
			if t < now-10*60*1000 {
				return
			}

			machine, err := queries.GetMachine(context.Background(), models.GetMachineParams{
				AccountID: sd.UserID,
				ID:        machineID,
			})
			if err != nil || machine.AccountID != sd.UserID {
				return
			}

			if err := queries.UpdateMachineActivity(context.Background(), models.UpdateMachineActivityParams{
				Active:       1,
				LastActiveAt: time.UnixMilli(t),
				AccountID:    sd.UserID,
				ID:           machineID,
			}); err != nil {
				log.Printf("Failed to update machine activity: %v", err)
			}

			ephemeral := map[string]any{
				"type":     "machine-activity",
				"id":       machineID,
				"active":   true,
				"activeAt": t,
			}
			s.emitEphemeralToUserScoped(sd.UserID, ephemeral, "")
		})

		// Machine metadata update
		client.On("machine-update-metadata", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			machineID := getString(payload, "machineId")
			metadata := getString(payload, "metadata")
			expected := getInt64(payload, "expectedVersion")
			if machineID == "" || metadata == "" {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid parameters"})
				}
				return
			}

			machine, err := queries.GetMachine(context.Background(), models.GetMachineParams{
				AccountID: sd.UserID,
				ID:        machineID,
			})
			if err != nil || machine.AccountID != sd.UserID {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Machine not found"})
				}
				return
			}

			if machine.MetadataVersion != expected {
				if ack != nil {
					ack(map[string]any{
						"result":   "version-mismatch",
						"version":  machine.MetadataVersion,
						"metadata": machine.Metadata,
					})
				}
				return
			}

			rows, err := queries.UpdateMachineMetadata(context.Background(), models.UpdateMachineMetadataParams{
				Metadata:          metadata,
				MetadataVersion:   expected + 1,
				AccountID:         sd.UserID,
				ID:                machineID,
				MetadataVersion_2: expected,
			})
			if err != nil || rows == 0 {
				current, err := queries.GetMachine(context.Background(), models.GetMachineParams{
					AccountID: sd.UserID,
					ID:        machineID,
				})
				if ack != nil {
					if err == nil {
						ack(map[string]any{
							"result":   "version-mismatch",
							"version":  current.MetadataVersion,
							"metadata": current.Metadata,
						})
					} else {
						ack(map[string]any{"result": "error"})
					}
				}
				return
			}

			if ack != nil {
				ack(map[string]any{
					"result":   "success",
					"version":  expected + 1,
					"metadata": metadata,
				})
			}

			userSeq, err := queries.UpdateAccountSeq(context.Background(), sd.UserID)
			if err != nil {
				log.Printf("Failed to allocate user seq: %v", err)
				return
			}

			updatePayload := map[string]any{
				"id":        pkgtypes.NewCUID(),
				"seq":       userSeq,
				"createdAt": time.Now().UnixMilli(),
				"body": map[string]any{
					"t":         "update-machine",
					"machineId": machineID,
					"metadata": map[string]any{
						"value":   metadata,
						"version": expected + 1,
					},
				},
			}
			s.emitUpdateToUser(sd.UserID, updatePayload, string(client.Id()))
		})

		// Machine daemon state update
		client.On("machine-update-state", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			machineID := getString(payload, "machineId")
			daemonState := getOptionalString(payload, "daemonState")
			expected := getInt64(payload, "expectedVersion")
			if machineID == "" || daemonState == nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid parameters"})
				}
				return
			}

			machine, err := queries.GetMachine(context.Background(), models.GetMachineParams{
				AccountID: sd.UserID,
				ID:        machineID,
			})
			if err != nil || machine.AccountID != sd.UserID {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Machine not found"})
				}
				return
			}

			if machine.DaemonStateVersion != expected {
				if ack != nil {
					resp := map[string]any{
						"result":  "version-mismatch",
						"version": machine.DaemonStateVersion,
					}
					if machine.DaemonState.Valid {
						resp["daemonState"] = machine.DaemonState.String
					}
					ack(resp)
				}
				return
			}

			stateVal := sql.NullString{Valid: true, String: *daemonState}
			rows, err := queries.UpdateMachineDaemonState(context.Background(), models.UpdateMachineDaemonStateParams{
				DaemonState:          stateVal,
				DaemonStateVersion:   expected + 1,
				AccountID:            sd.UserID,
				ID:                   machineID,
				DaemonStateVersion_2: expected,
			})
			if err != nil || rows == 0 {
				current, err := queries.GetMachine(context.Background(), models.GetMachineParams{
					AccountID: sd.UserID,
					ID:        machineID,
				})
				if ack != nil {
					if err == nil {
						resp := map[string]any{
							"result":  "version-mismatch",
							"version": current.DaemonStateVersion,
						}
						if current.DaemonState.Valid {
							resp["daemonState"] = current.DaemonState.String
						}
						ack(resp)
					} else {
						ack(map[string]any{"result": "error"})
					}
				}
				return
			}

			if ack != nil {
				ack(map[string]any{
					"result":      "success",
					"version":     expected + 1,
					"daemonState": *daemonState,
				})
			}

			userSeq, err := queries.UpdateAccountSeq(context.Background(), sd.UserID)
			if err != nil {
				log.Printf("Failed to allocate user seq: %v", err)
				return
			}

			updatePayload := map[string]any{
				"id":        pkgtypes.NewCUID(),
				"seq":       userSeq,
				"createdAt": time.Now().UnixMilli(),
				"body": map[string]any{
					"t":         "update-machine",
					"machineId": machineID,
					"daemonState": map[string]any{
						"value":   *daemonState,
						"version": expected + 1,
					},
				},
			}
			s.emitUpdateToUser(sd.UserID, updatePayload, string(client.Id()))
		})

		// Usage report event
		client.On("usage-report", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			sessionID := getString(payload, "sessionId")
			if sessionID == "" {
				return
			}

			session, err := queries.GetSessionByID(context.Background(), sessionID)
			if err != nil || session.AccountID != sd.UserID {
				return
			}

			key := getString(payload, "key")
			tokens := getMap(payload, "tokens")
			cost := getMap(payload, "cost")
			if key == "" || tokens == nil || cost == nil {
				return
			}

			ephemeral := map[string]any{
				"type":      "usage",
				"id":        sessionID,
				"key":       key,
				"tokens":    tokens,
				"cost":      cost,
				"timestamp": time.Now().UnixMilli(),
			}
			s.emitEphemeralToUser(sd.UserID, ephemeral, "")
		})

		// Ephemeral forward (client -> server -> user-scoped)
		client.On("ephemeral", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			if payload == nil {
				return
			}
			s.emitEphemeralToUserScoped(sd.UserID, payload, "")
		})

		// Access key get
		client.On("access-key-get", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			sessionID := getString(payload, "sessionId")
			machineID := getString(payload, "machineId")
			if sessionID == "" || machineID == "" {
				if ack != nil {
					ack(map[string]any{"ok": false, "error": "Invalid parameters: sessionId and machineId are required"})
				}
				return
			}

			session, err := queries.GetSessionByID(context.Background(), sessionID)
			if err != nil || session.AccountID != sd.UserID {
				if ack != nil {
					ack(map[string]any{"ok": false, "error": "Session or machine not found"})
				}
				return
			}
			machine, err := queries.GetMachine(context.Background(), models.GetMachineParams{
				AccountID: sd.UserID,
				ID:        machineID,
			})
			if err != nil || machine.AccountID != sd.UserID {
				if ack != nil {
					ack(map[string]any{"ok": false, "error": "Session or machine not found"})
				}
				return
			}

			accessKey, err := queries.GetAccessKey(context.Background(), models.GetAccessKeyParams{
				AccountID: sd.UserID,
				MachineID: machineID,
				SessionID: sessionID,
			})
			if ack != nil {
				if err == sql.ErrNoRows {
					ack(map[string]any{"ok": true, "accessKey": nil})
					return
				}
				if err != nil {
					ack(map[string]any{"ok": false, "error": "Internal error"})
					return
				}
				ack(map[string]any{
					"ok": true,
					"accessKey": map[string]any{
						"data":        accessKey.Data,
						"dataVersion": accessKey.DataVersion,
						"createdAt":   accessKey.CreatedAt.UnixMilli(),
						"updatedAt":   accessKey.UpdatedAt.UnixMilli(),
					},
				})
			}
		})

		// Artifact read
		client.On("artifact-read", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			artifactID := getString(payload, "artifactId")
			if artifactID == "" {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid parameters"})
				}
				return
			}

			artifact, err := queries.GetArtifactByIDAndAccount(context.Background(), models.GetArtifactByIDAndAccountParams{
				ID:        artifactID,
				AccountID: sd.UserID,
			})
			if err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Artifact not found"})
				}
				return
			}

			if ack != nil {
				ack(map[string]any{
					"result": "success",
					"artifact": map[string]any{
						"id":            artifact.ID,
						"header":        base64.StdEncoding.EncodeToString(artifact.Header),
						"headerVersion": artifact.HeaderVersion,
						"body":          base64.StdEncoding.EncodeToString(artifact.Body),
						"bodyVersion":   artifact.BodyVersion,
						"seq":           artifact.Seq,
						"createdAt":     artifact.CreatedAt.UnixMilli(),
						"updatedAt":     artifact.UpdatedAt.UnixMilli(),
					},
				})
			}
		})

		// Artifact create
		client.On("artifact-create", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			artifactID, idOk := payload["id"].(string)
			header, headerOk := payload["header"].(string)
			body, bodyOk := payload["body"].(string)
			dataKey, keyOk := payload["dataEncryptionKey"].(string)
			if !idOk || !headerOk || !bodyOk || !keyOk || artifactID == "" {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid parameters"})
				}
				return
			}

			existing, err := queries.GetArtifactByID(context.Background(), artifactID)
			if err == nil {
				if existing.AccountID != sd.UserID {
					if ack != nil {
						ack(map[string]any{"result": "error", "message": "Artifact with this ID already exists for another account"})
					}
					return
				}
				if ack != nil {
					ack(map[string]any{
						"result": "success",
						"artifact": map[string]any{
							"id":            existing.ID,
							"header":        base64.StdEncoding.EncodeToString(existing.Header),
							"headerVersion": existing.HeaderVersion,
							"body":          base64.StdEncoding.EncodeToString(existing.Body),
							"bodyVersion":   existing.BodyVersion,
							"seq":           existing.Seq,
							"createdAt":     existing.CreatedAt.UnixMilli(),
							"updatedAt":     existing.UpdatedAt.UnixMilli(),
						},
					})
				}
				return
			} else if err != sql.ErrNoRows {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Internal error"})
				}
				return
			}

			headerBytes, err := base64.StdEncoding.DecodeString(header)
			if err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid header encoding"})
				}
				return
			}
			bodyBytes, err := base64.StdEncoding.DecodeString(body)
			if err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid body encoding"})
				}
				return
			}
			dataKeyBytes, err := base64.StdEncoding.DecodeString(dataKey)
			if err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid dataEncryptionKey encoding"})
				}
				return
			}

			if err := queries.CreateArtifact(context.Background(), models.CreateArtifactParams{
				ID:                artifactID,
				AccountID:         sd.UserID,
				Header:            headerBytes,
				HeaderVersion:     1,
				Body:              bodyBytes,
				BodyVersion:       1,
				DataEncryptionKey: dataKeyBytes,
				Seq:               0,
			}); err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Internal error"})
				}
				return
			}

			artifact, err := queries.GetArtifactByIDAndAccount(context.Background(), models.GetArtifactByIDAndAccountParams{
				ID:        artifactID,
				AccountID: sd.UserID,
			})
			if err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Internal error"})
				}
				return
			}

			userSeq, err := queries.UpdateAccountSeq(context.Background(), sd.UserID)
			if err == nil {
				updatePayload := map[string]any{
					"id":        pkgtypes.NewCUID(),
					"seq":       userSeq,
					"createdAt": time.Now().UnixMilli(),
					"body": map[string]any{
						"t":                 "new-artifact",
						"artifactId":        artifact.ID,
						"seq":               artifact.Seq,
						"header":            base64.StdEncoding.EncodeToString(artifact.Header),
						"headerVersion":     artifact.HeaderVersion,
						"body":              base64.StdEncoding.EncodeToString(artifact.Body),
						"bodyVersion":       artifact.BodyVersion,
						"dataEncryptionKey": base64.StdEncoding.EncodeToString(artifact.DataEncryptionKey),
						"createdAt":         artifact.CreatedAt.UnixMilli(),
						"updatedAt":         artifact.UpdatedAt.UnixMilli(),
					},
				}
				s.emitUpdateToUser(sd.UserID, updatePayload, "")
			}

			if ack != nil {
				ack(map[string]any{
					"result": "success",
					"artifact": map[string]any{
						"id":            artifact.ID,
						"header":        base64.StdEncoding.EncodeToString(artifact.Header),
						"headerVersion": artifact.HeaderVersion,
						"body":          base64.StdEncoding.EncodeToString(artifact.Body),
						"bodyVersion":   artifact.BodyVersion,
						"seq":           artifact.Seq,
						"createdAt":     artifact.CreatedAt.UnixMilli(),
						"updatedAt":     artifact.UpdatedAt.UnixMilli(),
					},
				})
			}
		})

		// Artifact update
		client.On("artifact-update", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			artifactID := getString(payload, "artifactId")
			if artifactID == "" {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid parameters"})
				}
				return
			}

			headerPayload := getMap(payload, "header")
			bodyPayload := getMap(payload, "body")
			headerPresent := headerPayload != nil
			bodyPresent := bodyPayload != nil
			headerData := getString(headerPayload, "data")
			bodyData := getString(bodyPayload, "data")
			headerExpected := getInt64(headerPayload, "expectedVersion")
			bodyExpected := getInt64(bodyPayload, "expectedVersion")

			if headerPresent {
				if _, ok := headerPayload["data"].(string); !ok {
					if ack != nil {
						ack(map[string]any{"result": "error", "message": "Invalid header parameters"})
					}
					return
				}
				if _, ok := headerPayload["expectedVersion"]; !ok {
					if ack != nil {
						ack(map[string]any{"result": "error", "message": "Invalid header parameters"})
					}
					return
				}
			}
			if bodyPresent {
				if _, ok := bodyPayload["data"].(string); !ok {
					if ack != nil {
						ack(map[string]any{"result": "error", "message": "Invalid body parameters"})
					}
					return
				}
				if _, ok := bodyPayload["expectedVersion"]; !ok {
					if ack != nil {
						ack(map[string]any{"result": "error", "message": "Invalid body parameters"})
					}
					return
				}
			}

			if !headerPresent && !bodyPresent {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "No updates provided"})
				}
				return
			}

			current, err := queries.GetArtifactByIDAndAccount(context.Background(), models.GetArtifactByIDAndAccountParams{
				ID:        artifactID,
				AccountID: sd.UserID,
			})
			if err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Artifact not found"})
				}
				return
			}

			headerMismatch := headerPresent && current.HeaderVersion != headerExpected
			bodyMismatch := bodyPresent && current.BodyVersion != bodyExpected
			if headerMismatch || bodyMismatch {
				response := map[string]any{"result": "version-mismatch"}
				if headerMismatch {
					response["header"] = map[string]any{
						"currentVersion": current.HeaderVersion,
						"currentData":    base64.StdEncoding.EncodeToString(current.Header),
					}
				}
				if bodyMismatch {
					response["body"] = map[string]any{
						"currentVersion": current.BodyVersion,
						"currentData":    base64.StdEncoding.EncodeToString(current.Body),
					}
				}
				if ack != nil {
					ack(response)
				}
				return
			}

			var headerBytes []byte
			var bodyBytes []byte
			if headerPresent {
				headerBytes, err = base64.StdEncoding.DecodeString(headerData)
				if err != nil {
					if ack != nil {
						ack(map[string]any{"result": "error", "message": "Invalid header encoding"})
					}
					return
				}
			}
			if bodyPresent {
				bodyBytes, err = base64.StdEncoding.DecodeString(bodyData)
				if err != nil {
					if ack != nil {
						ack(map[string]any{"result": "error", "message": "Invalid body encoding"})
					}
					return
				}
			}

			rows, err := queries.UpdateArtifact(context.Background(), models.UpdateArtifactParams{
				Header:          headerBytes,
				Column2:         boolToInt64(headerPresent),
				HeaderVersion:   headerExpected + 1,
				Body:            bodyBytes,
				Column5:         boolToInt64(bodyPresent),
				BodyVersion:     bodyExpected + 1,
				ID:              artifactID,
				AccountID:       sd.UserID,
				Column9:         boolToInt64(headerPresent),
				HeaderVersion_2: headerExpected,
				Column11:        boolToInt64(bodyPresent),
				BodyVersion_2:   bodyExpected,
			})
			if err != nil || rows == 0 {
				current, err := queries.GetArtifactByIDAndAccount(context.Background(), models.GetArtifactByIDAndAccountParams{
					ID:        artifactID,
					AccountID: sd.UserID,
				})
				if ack != nil {
					if err == nil {
						response := map[string]any{"result": "version-mismatch"}
						if headerPresent {
							response["header"] = map[string]any{
								"currentVersion": current.HeaderVersion,
								"currentData":    base64.StdEncoding.EncodeToString(current.Header),
							}
						}
						if bodyPresent {
							response["body"] = map[string]any{
								"currentVersion": current.BodyVersion,
								"currentData":    base64.StdEncoding.EncodeToString(current.Body),
							}
						}
						ack(response)
					} else {
						ack(map[string]any{"result": "error", "message": "Internal error"})
					}
				}
				return
			}

			var headerUpdate map[string]any
			var bodyUpdate map[string]any
			if headerPresent {
				headerUpdate = map[string]any{
					"value":   headerData,
					"version": headerExpected + 1,
				}
			}
			if bodyPresent {
				bodyUpdate = map[string]any{
					"value":   bodyData,
					"version": bodyExpected + 1,
				}
			}

			userSeq, err := queries.UpdateAccountSeq(context.Background(), sd.UserID)
			if err == nil {
				updatePayload := map[string]any{
					"id":        pkgtypes.NewCUID(),
					"seq":       userSeq,
					"createdAt": time.Now().UnixMilli(),
					"body": map[string]any{
						"t":          "update-artifact",
						"artifactId": artifactID,
						"header":     headerUpdate,
						"body":       bodyUpdate,
					},
				}
				s.emitUpdateToUser(sd.UserID, updatePayload, "")
			}

			if ack != nil {
				response := map[string]any{"result": "success"}
				if headerUpdate != nil {
					response["header"] = map[string]any{
						"version": headerUpdate["version"],
						"data":    headerData,
					}
				}
				if bodyUpdate != nil {
					response["body"] = map[string]any{
						"version": bodyUpdate["version"],
						"data":    bodyData,
					}
				}
				ack(response)
			}
		})

		// Artifact delete
		client.On("artifact-delete", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			artifactID := getString(payload, "artifactId")
			if artifactID == "" {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Invalid parameters"})
				}
				return
			}

			_, err := queries.GetArtifactByIDAndAccount(context.Background(), models.GetArtifactByIDAndAccountParams{
				ID:        artifactID,
				AccountID: sd.UserID,
			})
			if err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Artifact not found"})
				}
				return
			}

			if err := queries.DeleteArtifact(context.Background(), models.DeleteArtifactParams{
				ID:        artifactID,
				AccountID: sd.UserID,
			}); err != nil {
				if ack != nil {
					ack(map[string]any{"result": "error", "message": "Internal error"})
				}
				return
			}

			userSeq, err := queries.UpdateAccountSeq(context.Background(), sd.UserID)
			if err == nil {
				updatePayload := map[string]any{
					"id":        pkgtypes.NewCUID(),
					"seq":       userSeq,
					"createdAt": time.Now().UnixMilli(),
					"body": map[string]any{
						"t":          "delete-artifact",
						"artifactId": artifactID,
					},
				}
				s.emitUpdateToUser(sd.UserID, updatePayload, "")
			}

			if ack != nil {
				ack(map[string]any{"result": "success"})
			}
		})
		// Update metadata event
		client.On("update-metadata", func(data ...any) {
			sd := s.getSocketData(client.Id())
			log.Printf("Update metadata from user %s (socket %s): %+v", sd.UserID, client.Id(), data)

			payload, ack := getFirstMapWithAck(data)
			sid := getString(payload, "sid")
			metadata := getString(payload, "metadata")
			expected := getInt64(payload, "expectedVersion")
			if sid == "" || metadata == "" {
				if ack != nil {
					ack(map[string]any{"result": "error"})
				}
				return
			}

			session, err := queries.GetSessionByID(context.Background(), sid)
			if err != nil || session.AccountID != sd.UserID {
				if ack != nil {
					ack(map[string]any{"result": "error"})
				}
				return
			}

			rows, err := queries.UpdateSessionMetadata(context.Background(), models.UpdateSessionMetadataParams{
				Metadata:          metadata,
				MetadataVersion:   expected + 1,
				ID:                sid,
				MetadataVersion_2: expected,
			})
			if err != nil || rows == 0 {
				current, err := queries.GetSessionByID(context.Background(), sid)
				if ack != nil {
					if err == nil {
						ack(map[string]any{
							"result":   "version-mismatch",
							"version":  current.MetadataVersion,
							"metadata": current.Metadata,
						})
					} else {
						ack(map[string]any{"result": "error"})
					}
				}
				return
			}

			if ack != nil {
				ack(map[string]any{
					"result":   "success",
					"version":  expected + 1,
					"metadata": metadata,
				})
			}

			userSeq, err := queries.UpdateAccountSeq(context.Background(), sd.UserID)
			if err != nil {
				log.Printf("Failed to allocate user seq: %v", err)
				return
			}

			s.emitUpdateToSession(sd.UserID, sid, protocolwire.UpdateEvent{
				ID:        pkgtypes.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateSession{
					T:  "update-session",
					ID: sid,
					Metadata: &protocolwire.VersionedString{
						Value:   metadata,
						Version: expected + 1,
					},
				},
			}, string(client.Id()))
		})

		// Update state event
		client.On("update-state", func(data ...any) {
			sd := s.getSocketData(client.Id())
			log.Printf("Update state from user %s (socket %s): %+v", sd.UserID, client.Id(), data)

			payload, ack := getFirstMapWithAck(data)
			sid := getString(payload, "sid")
			expected := getInt64(payload, "expectedVersion")
			agentState := getOptionalString(payload, "agentState")
			if sid == "" {
				if ack != nil {
					ack(map[string]any{"result": "error"})
				}
				return
			}

			session, err := queries.GetSessionByID(context.Background(), sid)
			if err != nil || session.AccountID != sd.UserID {
				if ack != nil {
					ack(map[string]any{"result": "error"})
				}
				return
			}

			stateVal := sql.NullString{}
			if agentState != nil {
				stateVal.Valid = true
				stateVal.String = *agentState
			}

			rows, err := queries.UpdateSessionAgentState(context.Background(), models.UpdateSessionAgentStateParams{
				AgentState:          stateVal,
				AgentStateVersion:   expected + 1,
				ID:                  sid,
				AgentStateVersion_2: expected,
			})
			if err != nil || rows == 0 {
				current, err := queries.GetSessionByID(context.Background(), sid)
				if ack != nil {
					if err == nil {
						ack(map[string]any{
							"result":     "version-mismatch",
							"version":    current.AgentStateVersion,
							"agentState": current.AgentState.String,
						})
					} else {
						ack(map[string]any{"result": "error"})
					}
				}
				return
			}

			if ack != nil {
				resp := map[string]any{
					"result":  "success",
					"version": expected + 1,
				}
				if agentState != nil {
					resp["agentState"] = *agentState
				}
				ack(resp)
			}

			userSeq, err := queries.UpdateAccountSeq(context.Background(), sd.UserID)
			if err != nil {
				log.Printf("Failed to allocate user seq: %v", err)
				return
			}

			var agentStateBody *protocolwire.VersionedString
			if agentState != nil {
				agentStateBody = &protocolwire.VersionedString{
					Value:   *agentState,
					Version: expected + 1,
				}
			}
			s.emitUpdateToSession(sd.UserID, sid, protocolwire.UpdateEvent{
				ID:        pkgtypes.NewCUID(),
				Seq:       userSeq,
				CreatedAt: time.Now().UnixMilli(),
				Body: protocolwire.UpdateBodyUpdateSession{
					T:          "update-session",
					ID:         sid,
					AgentState: agentStateBody,
				},
			}, string(client.Id()))
		})

		// RPC register
		client.On("rpc-register", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			method := getString(payload, "method")
			if method == "" {
				client.Emit("rpc-error", map[string]string{"type": "register", "error": "Invalid method name"})
				return
			}
			if shouldDebugRPC() {
				log.Printf("RPC register: user=%s client=%s method=%s", sd.UserID, sd.ClientType, method)
			}
			s.registerRPCMethod(sd.UserID, method, client)
			client.Emit("rpc-registered", map[string]string{"method": method})
		})

		// RPC unregister
		client.On("rpc-unregister", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, _ := getFirstMap(data)
			method := getString(payload, "method")
			if method == "" {
				client.Emit("rpc-error", map[string]string{"type": "unregister", "error": "Invalid method name"})
				return
			}
			s.unregisterRPCMethod(sd.UserID, method, client)
			client.Emit("rpc-unregistered", map[string]string{"method": method})
		})

		// RPC call
		client.On("rpc-call", func(data ...any) {
			sd := s.getSocketData(client.Id())
			payload, ack := getFirstMapWithAck(data)
			method := getString(payload, "method")
			params := getString(payload, "params")
			if method == "" {
				if ack != nil {
					ack(map[string]any{"ok": false, "error": "Invalid parameters: method is required"})
				}
				return
			}

			target := s.getRPCMethodSocket(sd.UserID, method)
			if target == nil {
				if ack != nil {
					ack(map[string]any{"ok": false, "error": "RPC method not available"})
				}
				return
			}
			if shouldDebugRPC() {
				log.Printf("RPC call: user=%s client=%s method=%s target=%s", sd.UserID, sd.ClientType, method, target.Id())
			}
			if target.Id() == client.Id() {
				if ack != nil {
					ack(map[string]any{"ok": false, "error": "Cannot call RPC on the same socket"})
				}
				return
			}

			target.Timeout(30*time.Second).EmitWithAck("rpc-request", map[string]any{
				"method": method,
				"params": params,
			})(func(args []any, err error) {
				if ack == nil {
					return
				}
				if err != nil {
					ack(map[string]any{"ok": false, "error": err.Error()})
					return
				}
				var result any
				if len(args) > 0 {
					result = args[0]
				}
				ack(map[string]any{"ok": true, "result": result})
			})
		})

		// Disconnection handler
		client.On("disconnect", func(data ...any) {
			sd := s.getSocketData(client.Id())
			reason := ""
			if len(data) > 0 {
				if r, ok := data[0].(string); ok {
					reason = r
				}
			}
			log.Printf("User disconnected: %s (socket %s, clientType: %s, reason: %s)",
				sd.UserID, client.Id(), sd.ClientType, reason)

			if sd.ClientType == "session-scoped" && sd.SessionID != "" {
				now := time.Now()
				if err := queries.UpdateSessionActivity(context.Background(), models.UpdateSessionActivityParams{
					Active:       0,
					LastActiveAt: now,
					ID:           sd.SessionID,
				}); err != nil {
					log.Printf("Failed to update session activity: %v", err)
				}
				ephemeral := map[string]any{
					"type":     "activity",
					"id":       sd.SessionID,
					"active":   false,
					"activeAt": now.UnixMilli(),
					"thinking": false,
				}
				s.emitEphemeralToUser(sd.UserID, ephemeral, "")
			}

			if sd.ClientType == "machine-scoped" && sd.MachineID != "" {
				now := time.Now()
				if err := queries.UpdateMachineActivity(context.Background(), models.UpdateMachineActivityParams{
					Active:       0,
					LastActiveAt: now,
					AccountID:    sd.UserID,
					ID:           sd.MachineID,
				}); err != nil {
					log.Printf("Failed to update machine activity: %v", err)
				}
				ephemeral := map[string]any{
					"type":     "machine-activity",
					"id":       sd.MachineID,
					"active":   false,
					"activeAt": now.UnixMilli(),
				}
				s.emitEphemeralToUserScoped(sd.UserID, ephemeral, "")
			}
			// Clean up socket data
			s.socketData.Delete(client.Id())
			s.unregisterAllRPCMethods(sd.UserID, client)
		})
	})
}

func decodeAny(input any, out any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (s *SocketIOServer) emitUpdateToSession(userID, sessionID string, payload any, skipSocketID string) {
	s.socketData.Range(func(key, value any) bool {
		targetSD, ok := value.(*SocketData)
		if !ok {
			return true
		}

		if skipSocketID != "" && key == skipSocketID {
			return true
		}

		if targetSD.UserID != userID || targetSD.Socket == nil {
			return true
		}

		if targetSD.ClientType == "machine-scoped" {
			return true
		}

		if targetSD.ClientType == "session-scoped" && targetSD.SessionID != sessionID {
			return true
		}

		log.Printf("Emitting update to %s client (socket %v)", targetSD.ClientType, key)
		targetSD.Socket.Emit("update", payload)
		return true
	})
}

func (s *SocketIOServer) emitEphemeralToUser(userID string, payload any, skipSocketID string) {
	s.emitEphemeralToUserWithFilter(userID, payload, skipSocketID, false)
}

func (s *SocketIOServer) emitEphemeralToUserScoped(userID string, payload any, skipSocketID string) {
	s.emitEphemeralToUserWithFilter(userID, payload, skipSocketID, true)
}

func (s *SocketIOServer) emitEphemeralToUserWithFilter(userID string, payload any, skipSocketID string, userScopedOnly bool) {
	s.socketData.Range(func(key, value any) bool {
		targetSD, ok := value.(*SocketData)
		if !ok {
			return true
		}
		if skipSocketID != "" && key == skipSocketID {
			return true
		}
		if targetSD.UserID != userID || targetSD.Socket == nil {
			return true
		}
		if userScopedOnly && targetSD.ClientType != "user-scoped" {
			return true
		}
		targetSD.Socket.Emit("ephemeral", payload)
		return true
	})
}

func (s *SocketIOServer) emitUpdateToUser(userID string, payload any, skipSocketID string) {
	s.socketData.Range(func(key, value any) bool {
		targetSD, ok := value.(*SocketData)
		if !ok {
			return true
		}
		if skipSocketID != "" && key == skipSocketID {
			return true
		}
		if targetSD.UserID != userID || targetSD.Socket == nil {
			return true
		}
		targetSD.Socket.Emit("update", payload)
		return true
	})
}

// EmitUpdateToUser exposes update emission for HTTP handlers.
func (s *SocketIOServer) EmitUpdateToUser(userID string, payload any) {
	s.emitUpdateToUser(userID, payload, "")
}

// EmitEphemeralToUser exposes ephemeral emission for HTTP handlers.
func (s *SocketIOServer) EmitEphemeralToUser(userID string, payload any) {
	s.emitEphemeralToUser(userID, payload, "")
}

func (s *SocketIOServer) registerRPCMethod(userID, method string, sock *socket.Socket) {
	s.rpcMu.Lock()
	defer s.rpcMu.Unlock()
	methods, ok := s.rpcMethods[userID]
	if !ok {
		methods = make(map[string]*socket.Socket)
		s.rpcMethods[userID] = methods
	}
	methods[method] = sock
}

func (s *SocketIOServer) unregisterRPCMethod(userID, method string, sock *socket.Socket) {
	s.rpcMu.Lock()
	defer s.rpcMu.Unlock()
	methods, ok := s.rpcMethods[userID]
	if !ok {
		return
	}
	if current, ok := methods[method]; ok && current != nil && current.Id() == sock.Id() {
		delete(methods, method)
	}
	if len(methods) == 0 {
		delete(s.rpcMethods, userID)
	}
}

func (s *SocketIOServer) unregisterAllRPCMethods(userID string, sock *socket.Socket) {
	s.rpcMu.Lock()
	defer s.rpcMu.Unlock()
	methods, ok := s.rpcMethods[userID]
	if !ok {
		return
	}
	for method, current := range methods {
		if current != nil && current.Id() == sock.Id() {
			delete(methods, method)
		}
	}
	if len(methods) == 0 {
		delete(s.rpcMethods, userID)
	}
}

func (s *SocketIOServer) getRPCMethodSocket(userID, method string) *socket.Socket {
	s.rpcMu.Lock()
	defer s.rpcMu.Unlock()
	methods, ok := s.rpcMethods[userID]
	if !ok {
		return nil
	}
	return methods[method]
}

func getFirstMap(data []any) (map[string]any, bool) {
	if len(data) == 0 {
		return nil, false
	}
	payload, ok := data[0].(map[string]any)
	return payload, ok
}

func getFirstMapWithAck(data []any) (map[string]any, func(...any)) {
	var ack func(...any)
	if len(data) == 0 {
		return nil, nil
	}
	if cb, ok := data[len(data)-1].(func(...any)); ok {
		ack = cb
		data = data[:len(data)-1]
	} else if cb, ok := data[len(data)-1].(socket.Ack); ok {
		ack = func(args ...any) {
			cb(args, nil)
		}
		data = data[:len(data)-1]
	}
	payload, _ := getFirstMap(data)
	return payload, ack
}

func shouldDebugRPC() bool {
	if val := os.Getenv("DELIGHT_DEBUG_RPC"); strings.EqualFold(val, "true") || strings.EqualFold(val, "1") {
		return true
	}
	return strings.EqualFold(os.Getenv("DELIGHT_DEBUG_RPC"), "true") ||
		strings.EqualFold(os.Getenv("DELIGHT_DEBUG_RPC"), "1")
}

func getString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if v, ok := payload[key].(string); ok {
		return v
	}
	return ""
}

func getOptionalString(payload map[string]any, key string) *string {
	if payload == nil {
		return nil
	}
	if v, ok := payload[key]; ok {
		if v == nil {
			return nil
		}
		if s, ok := v.(string); ok {
			return &s
		}
	}
	return nil
}

func getInt64(payload map[string]any, key string) int64 {
	if payload == nil {
		return 0
	}
	switch v := payload[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		i, _ := v.Int64()
		return i
	default:
		return 0
	}
}

func getBool(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	if v, ok := payload[key].(bool); ok {
		return v
	}
	return false
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func getMap(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	if v, ok := payload[key].(map[string]any); ok {
		return v
	}
	return nil
}

// getSocketData retrieves socket metadata by socket ID
func (s *SocketIOServer) getSocketData(socketID any) *SocketData {
	if data, ok := s.socketData.Load(socketID); ok {
		if sd, ok := data.(*SocketData); ok {
			return sd
		}
	}
	return &SocketData{} // Return empty struct if not found
}

// HandleSocketIO creates a Gin handler for Socket.IO
func (s *SocketIOServer) HandleSocketIO() gin.HandlerFunc {
	// Get the HTTP handler from Socket.IO server
	httpHandler := s.server.ServeHandler(nil)

	return func(c *gin.Context) {
		// Add CORS headers to match TypeScript server
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight
		if c.Request.Method == "OPTIONS" {
			c.Status(http.StatusOK)
			return
		}

		log.Printf("Socket.IO request: %s %s", c.Request.Method, c.Request.URL.Path)

		// Serve Socket.IO
		httpHandler.ServeHTTP(c.Writer, c.Request)
	}
}

// Close shuts down the Socket.IO server
func (s *SocketIOServer) Close() error {
	s.server.Close(nil)
	return nil
}
