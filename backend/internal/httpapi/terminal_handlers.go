package httpapi

import (
	"context"
	"io"
	"net/http"
	"sync"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/auth"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

func (a *API) serverTerminal(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	if _, err := auth.ParseToken(a.cfg.JWTSecret, tokenFromWS(r)); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", i18n.Text(lang, "api.wsAuthRequired"), nil)
		return
	}
	serverID := chi.URLParam(r, "id")
	server, err := a.store.GetServer(serverID, true)
	if err != nil {
		code := http.StatusInternalServerError
		if store.IsNotFound(err) {
			code = http.StatusNotFound
		}
		writeError(w, code, "SERVER_NOT_FOUND", err.Error(), nil)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }, Subprotocols: []string{"aifar.terminal"}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	target := server.Name
	if target == "" {
		target = server.Host
	}
	if err := a.sshTerminalWS(r.Context(), conn, server, target, lang); err != nil {
		var writeMu sync.Mutex
		writeTerminalLine(conn, &writeMu, "[error] "+err.Error())
	}
}

func (a *API) sshTerminalWS(ctx context.Context, conn *websocket.Conn, server store.Server, target, lang string) error {
	writeMu := &sync.Mutex{}
	client, err := adapter.DialSSH(ctx, server)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 40, 120, modes); err != nil {
		return err
	}
	if err := session.Shell(); err != nil {
		return err
	}
	writeTerminalLine(conn, writeMu, i18n.Text(lang, "api.connectedToTarget", target))

	streamDone := make(chan error, 3)
	go streamTerminalOutput(conn, writeMu, stdout, streamDone)
	go streamTerminalOutput(conn, writeMu, stderr, streamDone)
	go func() {
		streamDone <- session.Wait()
	}()

	readDone := make(chan error, 1)
	go func() {
		for {
			typ, msg, err := conn.ReadMessage()
			if err != nil {
				readDone <- err
				return
			}
			if typ == websocket.TextMessage || typ == websocket.BinaryMessage {
				if _, err := stdin.Write(msg); err != nil {
					readDone <- err
					return
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		_ = stdin.Close()
		_ = session.Close()
		return nil
	case err := <-readDone:
		_ = stdin.Close()
		_ = session.Close()
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
			return nil
		}
		return err
	case err := <-streamDone:
		_ = stdin.Close()
		_ = conn.Close()
		if err == io.EOF {
			return nil
		}
		return err
	}
}

func streamTerminalOutput(conn *websocket.Conn, writeMu *sync.Mutex, r io.Reader, done chan<- error) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			writeMu.Lock()
			writeErr := conn.WriteMessage(websocket.BinaryMessage, chunk)
			writeMu.Unlock()
			if writeErr != nil {
				done <- writeErr
				return
			}
		}
		if err != nil {
			if err == io.EOF {
				done <- nil
				return
			}
			done <- err
			return
		}
	}
}

func writeTerminalLine(conn *websocket.Conn, writeMu *sync.Mutex, line string) {
	writeMu.Lock()
	defer writeMu.Unlock()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(line+"\r\n"))
}
