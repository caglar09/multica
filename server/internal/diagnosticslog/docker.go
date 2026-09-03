package diagnosticslog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxDockerLogBody = 64 << 20

type Source struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Image  string `json:"image,omitempty"`
	State  string `json:"state,omitempty"`
	Status string `json:"status,omitempty"`
}

type Entry struct {
	Timestamp string `json:"timestamp,omitempty"`
	Source    string `json:"source"`
	Container string `json:"container,omitempty"`
	Stream    string `json:"stream"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

type Response struct {
	CollectedAt string   `json:"collected_at"`
	Sources     []Source `json:"sources"`
	Entries     []Entry  `json:"entries"`
}

type Query struct {
	Source string
	Search string
	Tail   int
	Since  int64
}

type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Status string            `json:"Status"`
	Labels map[string]string `json:"Labels"`
}

type DockerCollector struct {
	client         *http.Client
	project        string
	excludedSource string
}

func NewDockerCollector(socketPath, project, excludedSource string) *DockerCollector {
	if strings.TrimSpace(socketPath) == "" {
		socketPath = "/var/run/docker.sock"
	}
	if strings.TrimSpace(project) == "" {
		project = "multica"
	}
	if strings.TrimSpace(excludedSource) == "" {
		excludedSource = "diagnostics"
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &DockerCollector{
		client:         &http.Client{Transport: transport, Timeout: 30 * time.Second},
		project:        project,
		excludedSource: excludedSource,
	}
}

func (c *DockerCollector) Read(ctx context.Context, q Query) (Response, error) {
	if q.Tail <= 0 {
		q.Tail = 1000
	}
	containers, err := c.listContainers(ctx)
	if err != nil {
		return Response{}, err
	}

	sourceMap := map[string]Source{}
	for _, container := range containers {
		service := strings.TrimSpace(container.Labels["com.docker.compose.service"])
		if service == "" || service == c.excludedSource {
			continue
		}
		current, ok := sourceMap[service]
		if !ok || (current.State != "running" && container.State == "running") {
			sourceMap[service] = Source{
				ID: service, Label: service, Image: container.Image,
				State: container.State, Status: container.Status,
			}
		}
	}
	sources := make([]Source, 0, len(sourceMap))
	for _, source := range sourceMap {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })

	var entries []Entry
	for _, container := range containers {
		service := strings.TrimSpace(container.Labels["com.docker.compose.service"])
		if service == "" || service == c.excludedSource {
			continue
		}
		if q.Source != "" && service != q.Source {
			continue
		}
		containerEntries, err := c.readContainerLogs(ctx, container, service, q)
		if err != nil {
			entries = append(entries, Entry{
				Source: service, Container: cleanContainerName(container.Names),
				Stream: "stderr", Level: "error",
				Message: "diagnostics collector: " + err.Error(),
			})
			continue
		}
		entries = append(entries, containerEntries...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Timestamp == entries[j].Timestamp {
			return entries[i].Source < entries[j].Source
		}
		if entries[i].Timestamp == "" {
			return false
		}
		if entries[j].Timestamp == "" {
			return true
		}
		return entries[i].Timestamp < entries[j].Timestamp
	})

	return Response{
		CollectedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Sources: sources,
		Entries: entries,
	}, nil
}

func (c *DockerCollector) listContainers(ctx context.Context) ([]dockerContainer, error) {
	filters, _ := json.Marshal(map[string][]string{
		"label": {"com.docker.compose.project=" + c.project},
	})
	path := "/containers/json?all=1&filters=" + url.QueryEscape(string(filters))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker list containers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("docker list containers: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var containers []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("docker list containers decode: %w", err)
	}
	return containers, nil
}

func (c *DockerCollector) readContainerLogs(ctx context.Context, container dockerContainer, source string, q Query) ([]Entry, error) {
	values := url.Values{}
	values.Set("stdout", "1")
	values.Set("stderr", "1")
	values.Set("timestamps", "1")
	values.Set("tail", strconv.Itoa(q.Tail))
	if q.Since > 0 {
		values.Set("since", strconv.FormatInt(q.Since, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://docker/containers/"+url.PathEscape(container.ID)+"/logs?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker logs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("docker logs status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDockerLogBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxDockerLogBody {
		return nil, errors.New("docker log response exceeded 64 MiB safety limit")
	}
	return parseDockerLogBody(body, source, cleanContainerName(container.Names), q.Search), nil
}

func parseDockerLogBody(body []byte, source, container, search string) []Entry {
	if len(body) >= 8 && (body[0] == 1 || body[0] == 2) {
		var out []Entry
		for len(body) >= 8 {
			streamByte := body[0]
			size := int(binary.BigEndian.Uint32(body[4:8]))
			if size < 0 || len(body) < 8+size {
				break
			}
			stream := "stdout"
			if streamByte == 2 {
				stream = "stderr"
			}
			out = append(out, parseLines(body[8:8+size], source, container, stream, search)...)
			body = body[8+size:]
		}
		return out
	}
	return parseLines(body, source, container, "stdout", search)
}

func parseLines(body []byte, source, container, stream, search string) []Entry {
	needle := strings.ToLower(strings.TrimSpace(search))
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	var entries []Entry
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		timestamp, message := splitTimestamp(line)
		if needle != "" && !strings.Contains(strings.ToLower(message), needle) {
			continue
		}
		entries = append(entries, Entry{
			Timestamp: timestamp, Source: source, Container: container,
			Stream: stream, Level: detectLevel(message, stream), Message: message,
		})
	}
	return entries
}

func splitTimestamp(line string) (string, string) {
	first, rest, ok := strings.Cut(line, " ")
	if !ok {
		return "", line
	}
	if _, err := time.Parse(time.RFC3339Nano, first); err != nil {
		return "", line
	}
	return first, rest
}

func detectLevel(message, stream string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "\"level\":\"error\""), strings.Contains(lower, "level=error"),
		strings.Contains(lower, " error "), strings.HasPrefix(lower, "error"):
		return "error"
	case strings.Contains(lower, "\"level\":\"warn\""), strings.Contains(lower, "level=warn"),
		strings.Contains(lower, " warning "), strings.HasPrefix(lower, "warn"):
		return "warn"
	case strings.Contains(lower, "\"level\":\"debug\""), strings.Contains(lower, "level=debug"),
		strings.HasPrefix(lower, "debug"):
		return "debug"
	case stream == "stderr":
		return "info"
	default:
		return "info"
	}
}

func cleanContainerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
