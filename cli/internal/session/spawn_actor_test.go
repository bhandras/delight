package session

import "testing"

func TestSpawnActor_SpawnCompletesReply(t *testing.T) {
	t.Parallel()

	state := spawnActorState{OwnerSessionID: "owner"}
	reply := make(chan spawnResult, 1)

	next, effects := reduceSpawnActor(state, cmdSpawnChild{Directory: "/tmp", Agent: "claude", Reply: reply})
	if next.NextReq != 1 {
		t.Fatalf("NextReq=%d, want 1", next.NextReq)
	}
	if len(next.Pending) != 1 {
		t.Fatalf("pending=%d, want 1", len(next.Pending))
	}
	if len(effects) != 1 {
		t.Fatalf("effects=%d, want 1", len(effects))
	}
	if _, ok := effects[0].(effStartChild); !ok {
		t.Fatalf("effect=%T, want effStartChild", effects[0])
	}

	next2, effects2 := reduceSpawnActor(next, evChildStarted{ReqID: 1, SessionID: "child"})
	if len(effects2) != 0 {
		t.Fatalf("effects=%d, want 0", len(effects2))
	}
	if len(next2.Pending) != 0 {
		t.Fatalf("pending=%d, want 0", len(next2.Pending))
	}
	select {
	case res := <-reply:
		if res.Err != nil {
			t.Fatalf("err=%v, want nil", res.Err)
		}
		if res.SessionID != "child" {
			t.Fatalf("sessionID=%q, want child", res.SessionID)
		}
	default:
		t.Fatalf("expected reply")
	}
}

func TestSpawnActor_ShutdownBlocksFurtherSpawns(t *testing.T) {
	t.Parallel()

	state := spawnActorState{OwnerSessionID: "owner"}
	shutdownReply := make(chan spawnResult, 1)

	next, effects := reduceSpawnActor(state, cmdShutdownChildren{Reply: shutdownReply})
	if !next.Stopping {
		t.Fatalf("Stopping=false, want true")
	}
	if len(effects) != 1 {
		t.Fatalf("effects=%d, want 1", len(effects))
	}
	if _, ok := effects[0].(effShutdownChildren); !ok {
		t.Fatalf("effect=%T, want effShutdownChildren", effects[0])
	}

	spawnReply := make(chan spawnResult, 1)
	next2, effects2 := reduceSpawnActor(next, cmdSpawnChild{Directory: "/tmp", Agent: "claude", Reply: spawnReply})
	if len(effects2) != 0 {
		t.Fatalf("effects=%d, want 0", len(effects2))
	}
	if next2.NextReq != next.NextReq {
		t.Fatalf("NextReq changed: %d -> %d", next.NextReq, next2.NextReq)
	}
	select {
	case res := <-spawnReply:
		if res.Err == nil {
			t.Fatalf("expected error")
		}
	default:
		t.Fatalf("expected reply")
	}
}
