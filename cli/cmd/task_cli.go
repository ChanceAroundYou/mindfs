package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"mindfs/server/app"
)

func normalizeTaskRootFirstArgs(args []string) []string {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || !containsTaskFlag(args[1:]) {
		return args
	}
	out := make([]string, 0, len(args))
	out = append(out, args[1:]...)
	out = append(out, args[0])
	return out
}

func containsTaskFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-task" || arg == "--task" || strings.HasPrefix(arg, "-task=") || strings.HasPrefix(arg, "--task=") {
			return true
		}
	}
	return false
}

type taskCLIListResponse struct {
	Items []json.RawMessage `json:"items"`
}

type taskCLIDetailHeader struct {
	Task struct {
		ID         string `json:"id"`
		TaskNumber int    `json:"task_number"`
	} `json:"task"`
}

// handleTaskCommand 只读：打印指定编号的任务详情。阶段推进/暂停一律走面板。
func handleTaskCommand(addr string, useTLS bool, rootID, taskNumberRaw string) error {
	rootID = strings.TrimSpace(rootID)
	taskNumberRaw = strings.TrimSpace(strings.TrimPrefix(taskNumberRaw, "#"))
	if rootID == "" {
		return errors.New("root id argument required: mindfs <rootid> -task <task_number>")
	}
	taskNumber, err := strconv.Atoi(taskNumberRaw)
	if err != nil || taskNumber <= 0 {
		return errors.New("task number must be a positive integer")
	}
	token, err := app.ReadLocalCLIToken(addr)
	if err != nil {
		return err
	}
	_, detail, err := fetchTaskDetailByNumber(addr, useTLS, token, rootID, taskNumber)
	if err != nil {
		return err
	}
	if _, err = os.Stdout.Write(detail); err == nil {
		fmt.Fprintln(os.Stdout)
	}
	return err
}

func fetchTaskDetailByNumber(addr string, useTLS bool, token, rootID string, taskNumber int) (string, json.RawMessage, error) {
	query := url.Values{}
	query.Set("root", rootID)
	query.Set("task_number", strconv.Itoa(taskNumber))
	query.Set("limit", "1")
	req, err := http.NewRequest(http.MethodGet, addrToURL(addr, "/api/tasks?"+query.Encode(), useTLS), nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("X-MindFS-Local-CLI-Token", token)
	client := newHTTPClient(useTLS, 10*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("task lookup failed: %s", httpErrorMessage(resp))
	}
	var list taskCLIListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", nil, err
	}
	if len(list.Items) == 0 {
		return "", nil, fmt.Errorf("task #%d not found in root %s", taskNumber, rootID)
	}
	var header taskCLIDetailHeader
	if err := json.Unmarshal(list.Items[0], &header); err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(header.Task.ID) == "" || header.Task.TaskNumber != taskNumber {
		return "", nil, fmt.Errorf("invalid task lookup response for #%d", taskNumber)
	}
	return header.Task.ID, list.Items[0], nil
}

func httpErrorMessage(resp *http.Response) string {
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(payload))
	var apiErr struct {
		Error string `json:"error"`
	}
	if message != "" && json.Unmarshal(payload, &apiErr) == nil && strings.TrimSpace(apiErr.Error) != "" {
		message = strings.TrimSpace(apiErr.Error)
	}
	if message == "" {
		message = resp.Status
	}
	return message
}
