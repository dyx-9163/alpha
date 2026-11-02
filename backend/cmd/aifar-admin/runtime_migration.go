package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"aifar-deployment/backend/internal/i18n"
)

func runRuntimeMigrationCLI(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("migrate-runtime", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "http://127.0.0.1:8080", "panel URL")
	instance := flags.String("instance", "", "instance ID")
	reason := flags.String("reason", "", "audit reason")
	resume := flags.String("resume-task", "", "previous terminal migration task ID")
	statusOnly := flags.Bool("status", false, "read migration status")
	wait := flags.Bool("wait", false, "wait for task completion")
	timeout := flags.Duration("timeout", 10*time.Minute, "maximum wait")
	lang := flags.String("language", "zh", "zh or en")
	if err := flags.Parse(args); err != nil {
		return errors.New(i18n.Text(*lang, "aifar.migration.invalid"))
	}
	token := strings.TrimSpace(os.Getenv("AIFAR_API_TOKEN"))
	if token == "" {
		return errors.New(i18n.Text(*lang, "aifar.migration.tokenRequired"))
	}
	base, err := url.Parse(*server)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") || strings.TrimSpace(*instance) == "" || flags.NArg() != 0 || *timeout <= 0 {
		return errors.New(i18n.Text(*lang, "aifar.migration.invalid"))
	}
	host := base.Hostname()
	ip := net.ParseIP(host)
	if base.Scheme == "http" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New(i18n.Text(*lang, "aifar.migration.httpsRequired"))
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	request := func(method, path string, body any) ([]byte, error) {
		var input io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			input = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base.String(), "/")+"/api/v2"+path, input)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-AIFAR-Language", *lang)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return nil, errors.New(i18n.Text(*lang, "aifar.migration.requestFailed"))
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("%s: HTTP %d %s", i18n.Text(*lang, "aifar.migration.requestFailed"), response.StatusCode, strings.TrimSpace(string(data)))
		}
		return data, nil
	}
	method := http.MethodPost
	var body any = map[string]string{"reason": *reason, "resumeTaskId": *resume}
	if *statusOnly {
		method = http.MethodGet
		body = nil
	}
	data, err := request(method, "/apps/instances/"+url.PathEscape(strings.TrimSpace(*instance))+"/runtime/migration", body)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(out, string(data)); err != nil {
		return err
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err = json.Unmarshal(data, &accepted); err != nil {
		return err
	}
	if !*wait || accepted.TaskID == "" {
		return nil
	}
	for {
		data, err = request(http.MethodGet, "/tasks/"+url.PathEscape(accepted.TaskID), nil)
		if err != nil {
			return err
		}
		var result struct {
			Task struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"task"`
		}
		if err = json.Unmarshal(data, &result); err != nil {
			return err
		}
		switch result.Task.Status {
		case "success":
			_, err = fmt.Fprintln(out, string(data))
			return err
		case "failed", "cancelled", "timeout":
			return fmt.Errorf("%s: %s", i18n.Text(*lang, "aifar.migration.failed"), result.Task.Error)
		}
		select {
		case <-ctx.Done():
			return errors.New(i18n.Text(*lang, "aifar.migration.waitExpired"))
		case <-time.After(time.Second):
		}
	}
}
