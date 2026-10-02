package terminal

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// How often a pane checks which job is in its foreground.
const foregroundPoll = time.Second

type paneProcess struct {
	Pid  int    `json:"pid"`
	Name string `json:"name"`
}

func ServePane(conn *websocket.Conn, ts *Session) {
	master, ctl, _, err := OpenPane(ts.ctlPath, 80, 24)
	if err != nil {
		log.Printf("terminal: open pane failed session=%s: %v", ts.id, err)
		ts.detach()
		_ = conn.Close(websocket.StatusInternalError, "could not start shell")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var once sync.Once
	finish := func(reason string) {
		once.Do(func() {
			_ = master.Close()
			_ = ctl.Close()
			_ = conn.Close(websocket.StatusNormalClosure, reason)
			cancel()
			ts.detach()
		})
	}

	go func() {
		select {
		case <-ts.done:
			finish(ts.closeReason())
		case <-ctx.Done():
		}
	}()

	// The master alone would never EOF while a background process still holds the PTY slave open.
	go func() {
		reader := bufio.NewReader(ctl)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				finish("shell exited")
				return
			}
			var event paneEvent
			if json.Unmarshal(line, &event) == nil && event.Event == "exit" {
				finish("shell exited")
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 16<<10)
		for {
			n, readErr := master.Read(buf)
			if n > 0 {
				if conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					finish("connection closed")
					return
				}
			}
			if readErr != nil {
				finish("shell exited")
				return
			}
		}
	}()

	// Text frames tell the browser the foreground job's PIDs, so a grader can signal it from another pane.
	go func() {
		ticker := time.NewTicker(foregroundPoll)
		defer ticker.Stop()
		var last []paneProcess
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				procs := foregroundProcesses(master)
				if slices.Equal(procs, last) {
					continue
				}
				last = procs
				if procs == nil {
					procs = []paneProcess{}
				}
				payload, err := json.Marshal(struct {
					Type      string        `json:"type"`
					Processes []paneProcess `json:"processes"`
				}{"processes", procs})
				if err == nil && conn.Write(ctx, websocket.MessageText, payload) != nil {
					finish("connection closed")
					return
				}
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if conn.Ping(ctx) != nil {
					finish("connection lost")
					return
				}
			}
		}
	}()

	// Binary frames are raw input; text frames are control messages (resize).
	conn.SetReadLimit(1 << 20)
	for {
		kind, data, readErr := conn.Read(ctx)
		if readErr != nil {
			finish("connection closed")
			return
		}
		switch kind {
		case websocket.MessageBinary:
			ts.touch()
			if _, writeErr := master.Write(data); writeErr != nil {
				finish("shell exited")
				return
			}
		case websocket.MessageText:
			var msg struct {
				Type string `json:"type"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(master, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
			}
		}
	}
}
