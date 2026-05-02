// Package genieacs é o cliente HTTP do NBI (Northbound Interface) do GenieACS.
//
// O NBI fala um dialeto Mongo no /devices (filter via query string `query=`)
// e retorna documentos JSON com os caminhos TR-069 como chaves aninhadas.
//
// Convenções deste cliente:
//   - Endpoints retornam erro tipado quando o status >= 400.
//   - Time-outs configuráveis via http.Client (default 30s).
//   - Auth básica é opcional — se baseURL aponta para rede docker interna
//     sem auth, basta deixar user/pass vazios (caso default em prod).
package genieacs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client encapsula um http.Client + credenciais + baseURL do NBI.
// Campo `cache` é opcional — populado por WithCache (cache.go).
type Client struct {
	baseURL  string
	authUser string
	authPass string
	http     *http.Client
	cache    *Cache
}

// New constrói o cliente. Timeout default 30s — operações que demoram mais
// (ex: download de firmware grande) devem usar context com Deadline próprio.
func New(baseURL, user, pass string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		authUser: user,
		authPass: pass,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// SetHTTPClient permite injetar http.Client custom (testes, proxy, etc).
func (c *Client) SetHTTPClient(h *http.Client) { c.http = h }

// ──────────────── Health ────────────────

// Ping faz GET /devices?projection=_id&limit=1 — query mínima que valida
// conectividade + auth + resposta válida. Usada por /healthz.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.baseURL == "" {
		return errors.New("genieacs: cliente não configurado")
	}
	_, err := c.QueryDevices(ctx, Query{
		Projection: []string{"_id"},
		Limit:      1,
	})
	return err
}

// ──────────────── Devices ────────────────

// GetDevice busca 1 device pelo seu _id no GenieACS.
//
// Se cache estiver habilitado (WithCache), tenta primeiro o Redis com TTL
// configurado. Mutações (Set/Reboot/etc) invalidam automaticamente.
func (c *Client) GetDevice(ctx context.Context, deviceID string) (*Device, error) {
	if raw, ok := c.cacheGet(ctx, deviceID); ok {
		d := parseDevice(raw)
		return &d, nil
	}

	devices, err := c.QueryDevices(ctx, Query{
		Filter: map[string]any{"_id": deviceID},
		Limit:  1,
	})
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, ErrDeviceNotFound
	}
	c.cacheSet(ctx, deviceID, devices[0].Raw)
	return &devices[0], nil
}

// QueryDevices executa GET /devices com filtro Mongo.
func (c *Client) QueryDevices(ctx context.Context, q Query) ([]Device, error) {
	u, err := url.Parse(c.baseURL + "/devices/")
	if err != nil {
		return nil, err
	}
	params := u.Query()
	if q.Filter != nil {
		f, err := json.Marshal(q.Filter)
		if err != nil {
			return nil, fmt.Errorf("genieacs: marshal filter: %w", err)
		}
		params.Set("query", string(f))
	}
	if len(q.Projection) > 0 {
		params.Set("projection", strings.Join(q.Projection, ","))
	}
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Skip > 0 {
		params.Set("skip", strconv.Itoa(q.Skip))
	}
	if len(q.Sort) > 0 {
		s, err := json.Marshal(q.Sort)
		if err != nil {
			return nil, fmt.Errorf("genieacs: marshal sort: %w", err)
		}
		params.Set("sort", string(s))
	}
	u.RawQuery = params.Encode()

	resp, err := c.do(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errFromResponse("query devices", resp)
	}

	var raws []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raws); err != nil {
		return nil, fmt.Errorf("genieacs: decode: %w", err)
	}

	out := make([]Device, 0, len(raws))
	for _, raw := range raws {
		out = append(out, parseDevice(raw))
	}
	return out, nil
}

// DeleteDevice remove o registro do GenieACS — irreversível.
func (c *Client) DeleteDevice(ctx context.Context, deviceID string) error {
	defer c.InvalidateDevice(ctx, deviceID)
	u := c.baseURL + "/devices/" + url.PathEscape(deviceID)
	resp, err := c.do(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return errFromResponse("delete device", resp)
	}
	return nil
}

// ──────────────── Tasks ────────────────

// SetParameterValues enfileira mudanças e dispara connection request.
func (c *Client) SetParameterValues(ctx context.Context, deviceID string, params []Parameter) (TaskID, error) {
	pv := make([][]any, len(params))
	for i, p := range params {
		pv[i] = p.Tuple()
	}
	return c.postTask(ctx, deviceID, Task{
		Name:            "setParameterValues",
		ParameterValues: pv,
	})
}

// GetParameterValues força o ACS a perguntar ao CPE valores específicos.
func (c *Client) GetParameterValues(ctx context.Context, deviceID string, paths []string) (TaskID, error) {
	return c.postTask(ctx, deviceID, Task{
		Name:           "getParameterValues",
		ParameterNames: paths,
	})
}

// Refresh revalida uma sub-árvore inteira de parâmetros (ObjectName=="" → root).
func (c *Client) Refresh(ctx context.Context, deviceID, objectName string) (TaskID, error) {
	return c.postTask(ctx, deviceID, Task{
		Name:       "refreshObject",
		ObjectName: objectName,
	})
}

// Reboot agenda reinicialização do CPE.
func (c *Client) Reboot(ctx context.Context, deviceID string) (TaskID, error) {
	return c.postTask(ctx, deviceID, Task{Name: "reboot"})
}

// FactoryReset — operação destrutiva. Caller deve exigir confirmação extra.
func (c *Client) FactoryReset(ctx context.Context, deviceID string) (TaskID, error) {
	return c.postTask(ctx, deviceID, Task{Name: "factoryReset"})
}

// Download instrui o CPE a baixar firmware/config do GenieACS-FS.
func (c *Client) Download(ctx context.Context, deviceID, fileType, fileName, fileURL string) (TaskID, error) {
	return c.postTask(ctx, deviceID, Task{
		Name:     "download",
		FileType: fileType,
		FileName: fileName,
		URL:      fileURL,
	})
}

// ConnectionRequest força o ACS a "acordar" o CPE imediatamente, sem
// criar task significativa (refreshObject vazio é o no-op canônico).
func (c *Client) ConnectionRequest(ctx context.Context, deviceID string) error {
	_, err := c.postTask(ctx, deviceID, Task{
		Name:       "refreshObject",
		ObjectName: "",
	})
	return err
}

// GetTask consulta o estado atual de uma task (pendente, faulted, done — done
// some da listagem; quando não retorna nada, considere concluída).
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	u, err := url.Parse(c.baseURL + "/tasks/")
	if err != nil {
		return nil, err
	}
	params := u.Query()
	filter, _ := json.Marshal(map[string]any{"_id": taskID})
	params.Set("query", string(filter))
	params.Set("limit", "1")
	u.RawQuery = params.Encode()

	resp, err := c.do(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errFromResponse("get task", resp)
	}

	var tasks []Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("genieacs: decode: %w", err)
	}
	if len(tasks) == 0 {
		return nil, ErrTaskNotFound
	}
	return &tasks[0], nil
}

// ──────────────── Faults ────────────────

// GetFaults lista falhas registradas no GenieACS para um device.
func (c *Client) GetFaults(ctx context.Context, deviceID string) ([]Fault, error) {
	u, err := url.Parse(c.baseURL + "/faults/")
	if err != nil {
		return nil, err
	}
	params := u.Query()
	filter, _ := json.Marshal(map[string]any{"device": deviceID})
	params.Set("query", string(filter))
	u.RawQuery = params.Encode()

	resp, err := c.do(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errFromResponse("get faults", resp)
	}
	var out []Fault
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("genieacs: decode: %w", err)
	}
	return out, nil
}

// ──────────────── Internals ────────────────

// postTask é o factory de todas as ações enfileiráveis.
// Sempre adiciona ?connection_request — GenieACS dispara CR antes de
// tentar executar a task. (Caso futuro precise não-CR, parametrizar de novo.)
//
// Side-effect: invalida cache do device — próximo GetDevice retorna fresco.
func (c *Client) postTask(ctx context.Context, deviceID string, task Task) (TaskID, error) {
	defer c.InvalidateDevice(ctx, deviceID)

	u := fmt.Sprintf("%s/devices/%s/tasks?connection_request", c.baseURL, url.PathEscape(deviceID))

	body, err := json.Marshal(task)
	if err != nil {
		return "", err
	}

	resp, err := c.do(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		// ok
	default:
		return "", errFromResponse("post task", resp)
	}

	var result Task
	if resp.StatusCode != http.StatusNoContent && resp.ContentLength != 0 {
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("genieacs: decode task: %w", err)
		}
	}
	return TaskID(result.ID), nil
}

func (c *Client) do(ctx context.Context, method, urlStr string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, fmt.Errorf("genieacs: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authUser != "" {
		req.SetBasicAuth(c.authUser, c.authPass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("genieacs: do %s: %w", method, err)
	}
	return resp, nil
}

func errFromResponse(op string, resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return &APIError{
		Op:     op,
		Status: resp.StatusCode,
		Body:   string(bytes.TrimSpace(body)),
	}
}

// parseDevice extrai os campos canônicos do JSON do NBI e mantém o restante
// em Raw para acesso a paths arbitrários (virtual params, etc).
func parseDevice(raw map[string]any) Device {
	d := Device{Raw: raw}
	if id, ok := raw["_id"].(string); ok {
		d.ID = id
	}
	if li, ok := raw["_lastInform"].(string); ok {
		if t, err := time.Parse(time.RFC3339, li); err == nil {
			d.LastInform = t
		}
	}
	if lb, ok := raw["_lastBoot"].(string); ok {
		if t, err := time.Parse(time.RFC3339, lb); err == nil {
			d.LastBoot = &t
		}
	}
	if tags, ok := raw["_tags"].([]any); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok {
				d.Tags = append(d.Tags, s)
			}
		}
	}
	return d
}
