package terminal

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/web/connection"
)

func ForwardTerminal(id string, browser, agent *connection.SafeConn) {
	forwardTerminal(id, browser, agent, auditlog.Log)
}

func forwardTerminal(id string, browser, agent *connection.SafeConn, audit func(string, string, string, string)) {
	if browser == nil || agent == nil {
		return
	}
	TerminalSessionsMutex.Lock()
	session := TerminalSessions[id]
	requesterIp, userUUID := "", ""
	if session != nil && session.Browser == browser && session.Agent == agent {
		requesterIp, userUUID = session.RequesterIp, session.UserUUID
	}
	TerminalSessionsMutex.Unlock()
	if requesterIp == "" {
		return
	}

	audit(requesterIp, userUUID, "established, terminal id:"+id, "terminal")
	established_time := time.Now()
	errChan := make(chan error, 2)
	stopBrowser := keepAlive(browser, terminalPingInterval, terminalReadWait, terminalWriteWait)
	stopAgent := keepAlive(agent, terminalPingInterval, terminalReadWait, terminalWriteWait)
	defer stopBrowser()
	defer stopAgent()
	var readers sync.WaitGroup
	readers.Add(2)

	go func() {
		defer readers.Done()
		for {
			messageType, data, err := browser.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			_ = browser.SetReadDeadline(time.Now().Add(terminalReadWait))

			if messageType == websocket.TextMessage {
				var control struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(data, &control) == nil {
					if control.Type == "heartbeat" {
						continue
					}
					if control.Type == "close" {
						_ = agent.WriteJSON(gin.H{"type": "close"})
						closeSessionIfOwned(id, browser, agent)
						errChan <- nil
						return
					}
				}
				if len(data) > 0 && data[0] == '{' {
					err = agent.WriteMessage(websocket.TextMessage, data)
				} else {
					err = agent.WriteMessage(websocket.BinaryMessage, data)
				}
			} else {
				err = agent.WriteMessage(websocket.BinaryMessage, data)
			}

			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	go func() {
		defer readers.Done()
		for {
			kind, data, err := agent.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			_ = agent.SetReadDeadline(time.Now().Add(terminalReadWait))
			// Preserve only structured execution controls as text; ordinary terminal output stays binary.
			outKind := websocket.BinaryMessage
			if kind == websocket.TextMessage {
				var v struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(data, &v) == nil && strings.HasPrefix(v.Type, "mcp_") {
					outKind = websocket.TextMessage
				}
			}
			err = browser.WriteMessage(outKind, data)
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	// 等待错误或主动关闭
	<-errChan
	suspendSession(id, browser, agent)
	// Replacement has already retired both old sockets. Closing them here is
	// also safe when ownership changed, and guarantees both old readers exit.
	_ = browser.Close()
	_ = agent.Close()
	readers.Wait()
	disconnect_time := time.Now()
	audit(requesterIp, userUUID, "disconnected, terminal id:"+id+", duration:"+disconnect_time.Sub(established_time).String(), "terminal")
}
