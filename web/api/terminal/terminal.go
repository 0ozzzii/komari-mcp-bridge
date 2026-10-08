package terminal

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/komari-monitor/komari/web/connection"
)

type TerminalSession struct {
	Execution         json.RawMessage
	UUID              string
	UserUUID          string
	Browser           *connection.SafeConn
	Agent             *connection.SafeConn
	RequesterIp       string
	Forwarding        bool
	CleanupTimer      *time.Timer
	CleanupGeneration uint64
}

var TerminalSessionsMutex = &sync.Mutex{}
var TerminalSessions = make(map[string]*TerminalSession)

// 与 v2 事件队列 TTL 对齐，被控端短暂离线后会话仍可恢复。
const terminalSessionRetention = 5 * time.Minute

func scheduleCleanup(id string, session *TerminalSession) {
	session.CleanupGeneration++
	generation := session.CleanupGeneration
	if session.CleanupTimer != nil {
		session.CleanupTimer.Stop()
	}
	session.CleanupTimer = time.AfterFunc(terminalSessionRetention, func() {
		var browser, agent *connection.SafeConn

		TerminalSessionsMutex.Lock()
		if current, ok := TerminalSessions[id]; !ok || current != session ||
			session.CleanupGeneration != generation || (session.Browser != nil && session.Agent != nil) {
			TerminalSessionsMutex.Unlock()
			return
		}
		browser, agent = session.Browser, session.Agent
		delete(TerminalSessions, id)
		TerminalSessionsMutex.Unlock()

		if browser != nil {
			_ = browser.Close()
		}
		if agent != nil {
			_ = agent.Close()
		}
	})
}

func stopCleanup(session *TerminalSession) {
	session.CleanupGeneration++
	if session.CleanupTimer != nil {
		session.CleanupTimer.Stop()
		session.CleanupTimer = nil
	}
}

func suspendSession(id string, browser, agent *connection.SafeConn) {
	var otherBrowser, otherAgent *connection.SafeConn

	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil ||
		(browser != nil && session.Browser != browser) ||
		(agent != nil && session.Agent != agent) {
		TerminalSessionsMutex.Unlock()
		return
	}
	otherBrowser, otherAgent = session.Browser, session.Agent
	session.Browser = nil
	session.Agent = nil
	session.Forwarding = false
	scheduleCleanup(id, session)
	TerminalSessionsMutex.Unlock()

	if otherBrowser != nil {
		_ = otherBrowser.Close()
	}
	if otherAgent != nil {
		_ = otherAgent.Close()
	}
}

func closeSession(id string) {
	closeSessionIfOwned(id, nil, nil)
}

func closeSessionIfOwned(id string, ownerBrowser, ownerAgent *connection.SafeConn) {
	var browser, agent *connection.SafeConn

	TerminalSessionsMutex.Lock()
	if session, ok := TerminalSessions[id]; ok && session != nil &&
		(ownerBrowser == nil || session.Browser == ownerBrowser) &&
		(ownerAgent == nil || session.Agent == ownerAgent) {
		stopCleanup(session)
		browser, agent = session.Browser, session.Agent
		delete(TerminalSessions, id)
	}
	TerminalSessionsMutex.Unlock()

	if browser != nil {
		_ = browser.Close()
	}
	if agent != nil {
		_ = agent.Close()
	}
}

func attachBrowser(id, userUUID string, apiKey bool, conn *connection.SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil {
		TerminalSessionsMutex.Unlock()
		return nil, false
	}
	if !apiKey && session.UserUUID != userUUID {
		TerminalSessionsMutex.Unlock()
		return nil, false
	}
	oldBrowser := session.Browser
	var oldAgent *connection.SafeConn
	if oldBrowser != nil && oldBrowser != conn {
		// Retire both network halves. Keeping the old agent connection would
		// leave its previous ReadMessage alive while the new forwarder starts.
		oldAgent, session.Agent = session.Agent, nil
	}
	session.Browser = conn
	session.Forwarding = false
	if session.Agent != nil {
		stopCleanup(session)
	} else {
		scheduleCleanup(id, session)
	}
	TerminalSessionsMutex.Unlock()
	if oldBrowser != nil && oldBrowser != conn {
		_ = oldBrowser.Close()
	}
	if oldAgent != nil {
		_ = oldAgent.Close()
	}
	return session, true
}

func attachAgent(id string, conn *connection.SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil {
		TerminalSessionsMutex.Unlock()
		return nil, false
	}
	oldAgent := session.Agent
	var oldBrowser *connection.SafeConn
	if oldAgent != nil && oldAgent != conn {
		oldBrowser, session.Browser = session.Browser, nil
	}
	session.Agent = conn
	session.Forwarding = false
	if session.Browser != nil {
		stopCleanup(session)
	} else {
		scheduleCleanup(id, session)
	}
	TerminalSessionsMutex.Unlock()
	if oldAgent != nil && oldAgent != conn {
		_ = oldAgent.Close()
	}
	if oldBrowser != nil {
		_ = oldBrowser.Close()
	}
	return session, true
}

func hasAgent(id string, browser *connection.SafeConn) bool {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	s := TerminalSessions[id]
	return s != nil && s.Browser == browser && s.Agent != nil
}

func maybeStartForwarding(id string) {
	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil || session.Browser == nil || session.Agent == nil || session.Forwarding {
		TerminalSessionsMutex.Unlock()
		return
	}
	session.Forwarding = true
	browser, agent := session.Browser, session.Agent
	TerminalSessionsMutex.Unlock()
	go ForwardTerminal(id, browser, agent)
}
