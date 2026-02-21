package webclient

import "encoding/json"

// sdkListener bridges SDK callbacks into stream events and debug logs.
type sdkListener struct {
	app *App
}

// newSDKListener creates an SDK callback adapter for the bridge.
func newSDKListener(app *App) *sdkListener {
	return &sdkListener{app: app}
}

// OnConnected notifies clients about SDK socket connectivity.
func (l *sdkListener) OnConnected() {
	l.app.setSDKConnected(true)
	l.app.logInfo("sdk connected")
	l.app.publish("connected", streamPublishPayload{Reason: "sdk-connected"})
}

// OnDisconnected notifies clients when SDK socket disconnects.
func (l *sdkListener) OnDisconnected(reason string) {
	l.app.setSDKConnected(false)
	l.app.logInfo("sdk disconnected: " + reason)
	l.app.publish("disconnected", streamPublishPayload{Reason: reason})
}

// OnUpdate forwards SDK updates to stream subscribers.
func (l *sdkListener) OnUpdate(sessionID string, updateJSON string) {
	var decoded interface{}
	if err := json.Unmarshal([]byte(updateJSON), &decoded); err != nil {
		decoded = map[string]string{"raw": updateJSON}
	}
	l.app.publish("update", streamPublishPayload{SessionID: sessionID, Update: decoded})
}

// OnError forwards SDK errors to stream subscribers.
func (l *sdkListener) OnError(message string) {
	l.app.logError("sdk error: " + message)
}
