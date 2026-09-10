package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	CanvasNodeTypeText         = "text"
	CanvasNodeTypeImage        = "image"
	CanvasNodeTypePrompt       = "prompt"
	CanvasNodeTypeLoop         = "loop"
	CanvasNodeTypeGroup        = "group"
	CanvasNodeTypeTextToImage  = "text_to_image"
	CanvasNodeTypeImageToImage = "image_to_image"
	CanvasNodeTypeResult       = "result"

	CanvasRunStatusPending   = "pending"
	CanvasRunStatusRunning   = "running"
	CanvasRunStatusSucceeded = "succeeded"
	CanvasRunStatusFailed    = "failed"
	CanvasRunStatusCanceled  = "canceled"

	defaultCanvasTitle          = "Untitled canvas"
	defaultCanvasRunTriggerType = "manual"
	defaultCanvasImageModel     = "gpt-image-2"
	defaultCanvasChatModel      = "gpt-5.4"
	maxCanvasTitleLength        = 200
	maxCanvasDescriptionLength  = 4000
	maxCanvasNodeCount          = 256
	maxCanvasEdgeCount          = 512
	maxCanvasJSONBytes          = 256 << 10
)

var canvasNodeTypes = []string{
	CanvasNodeTypeText,
	CanvasNodeTypeImage,
	CanvasNodeTypePrompt,
	CanvasNodeTypeLoop,
	CanvasNodeTypeGroup,
	CanvasNodeTypeTextToImage,
	CanvasNodeTypeImageToImage,
	CanvasNodeTypeResult,
}

var canvasNodeTypeSet = map[string]struct{}{
	CanvasNodeTypeText:         {},
	CanvasNodeTypeImage:        {},
	CanvasNodeTypePrompt:       {},
	CanvasNodeTypeLoop:         {},
	CanvasNodeTypeGroup:        {},
	CanvasNodeTypeTextToImage:  {},
	CanvasNodeTypeImageToImage: {},
	CanvasNodeTypeResult:       {},
}

type CanvasDocument struct {
	ID          int64          `json:"id"`
	UserID      int64          `json:"user_id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Nodes       []CanvasNode   `json:"nodes"`
	Edges       []CanvasEdge   `json:"edges"`
	Viewport    map[string]any `json:"viewport"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type CanvasListItem struct {
	ID          int64          `json:"id"`
	UserID      int64          `json:"user_id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Viewport    map[string]any `json:"viewport"`
	Metadata    map[string]any `json:"metadata"`
	NodeCount   int            `json:"node_count"`
	EdgeCount   int            `json:"edge_count"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type CanvasNode struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Position map[string]any `json:"position"`
	Data     map[string]any `json:"data"`
	Metadata map[string]any `json:"metadata"`
}

type CanvasEdge struct {
	ID           string         `json:"id"`
	Source       string         `json:"source"`
	Target       string         `json:"target"`
	SourceHandle string         `json:"source_handle,omitempty"`
	TargetHandle string         `json:"target_handle,omitempty"`
	Data         map[string]any `json:"data"`
	Metadata     map[string]any `json:"metadata"`
}

type CanvasSaveInput struct {
	ID          int64          `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Nodes       []CanvasNode   `json:"nodes"`
	Edges       []CanvasEdge   `json:"edges"`
	Viewport    map[string]any `json:"viewport"`
	Metadata    map[string]any `json:"metadata"`
}

type CanvasListFilters struct {
	Limit  int
	Offset int
}

type CanvasRun struct {
	ID           int64          `json:"id"`
	UserID       int64          `json:"user_id"`
	CanvasID     int64          `json:"canvas_id,omitempty"`
	Status       string         `json:"status"`
	TriggerType  string         `json:"trigger_type"`
	APIKeyID     int64          `json:"api_key_id,omitempty"`
	Model        string         `json:"model"`
	Input        map[string]any `json:"input"`
	Output       map[string]any `json:"output"`
	ErrorMessage string         `json:"error_message,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	CanceledAt   *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type CanvasRunCreateInput struct {
	CanvasID    int64          `json:"canvas_id"`
	APIKeyID    int64          `json:"api_key_id"`
	Model       string         `json:"model"`
	TriggerType string         `json:"trigger_type"`
	Input       map[string]any `json:"input"`
	Metadata    map[string]any `json:"metadata"`
}

type CanvasRunCompleteInput struct {
	Status       string
	Output       map[string]any
	ErrorMessage string
}

type CanvasRunListFilters struct {
	CanvasID int64
	Limit    int
	Offset   int
}

type CanvasModelCatalog struct {
	Object            string             `json:"object"`
	Items             []ModelCatalogItem `json:"items"`
	ChatModels        []string           `json:"chat_models"`
	ImageModels       []string           `json:"image_models"`
	NodeTypes         []string           `json:"node_types"`
	DefaultChatModel  string             `json:"default_chat_model"`
	DefaultImageModel string             `json:"default_image_model"`
}

type CanvasRepository interface {
	ListCanvases(ctx context.Context, userID int64, filters CanvasListFilters) ([]CanvasListItem, int, error)
	GetCanvas(ctx context.Context, userID int64, canvasID int64) (*CanvasDocument, error)
	SaveCanvas(ctx context.Context, userID int64, input CanvasSaveInput) (*CanvasDocument, error)
	DeleteCanvas(ctx context.Context, userID int64, canvasID int64) error
	CreateCanvasRun(ctx context.Context, userID int64, input CanvasRunCreateInput) (*CanvasRun, error)
	MarkCanvasRunRunning(ctx context.Context, userID int64, runID int64) (*CanvasRun, error)
	CompleteCanvasRun(ctx context.Context, userID int64, runID int64, input CanvasRunCompleteInput) (*CanvasRun, error)
	ListCanvasRuns(ctx context.Context, userID int64, filters CanvasRunListFilters) ([]CanvasRun, int, error)
	GetCanvasRun(ctx context.Context, userID int64, runID int64) (*CanvasRun, error)
	CancelCanvasRun(ctx context.Context, userID int64, runID int64) (*CanvasRun, error)
}

type CanvasImageTaskCreator interface {
	CreateTask(ctx context.Context, userID int64, input ImageCreatorCreateTaskInput) (*ImageCreatorTask, error)
	GetReferenceImageForUser(ctx context.Context, userID int64, imageID int64) (*ImageCreatorFile, error)
}

type CanvasImageTaskCanceler interface {
	CancelTasksForUser(ctx context.Context, userID int64, taskIDs []int64) (int, error)
}

type CanvasService struct {
	repo           CanvasRepository
	imageCreator   CanvasImageTaskCreator
	apiKeys        *APIKeyService
	channelService *ChannelService
}

func NewCanvasService(repo CanvasRepository) *CanvasService {
	return &CanvasService{repo: repo}
}

func NewCanvasServiceWithDeps(repo CanvasRepository, imageCreator CanvasImageTaskCreator, apiKeys ...*APIKeyService) *CanvasService {
	svc := &CanvasService{repo: repo, imageCreator: imageCreator}
	if len(apiKeys) > 0 {
		svc.apiKeys = apiKeys[0]
	}
	return svc
}

// SetChannelService supplies the live channel catalog used to discover models
// available through an API Key's primary and multi-route groups.
func (s *CanvasService) SetChannelService(channelService *ChannelService) {
	if s != nil {
		s.channelService = channelService
	}
}

func (s *CanvasService) ListCanvases(ctx context.Context, userID int64, filters CanvasListFilters) ([]CanvasListItem, int, error) {
	if err := validateCanvasUser(userID); err != nil {
		return nil, 0, err
	}
	filters = normalizeCanvasListFilters(filters)
	items, total, err := s.repo.ListCanvases(ctx, userID, filters)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		normalizeCanvasSummaryJSON(&items[i])
	}
	return items, total, nil
}

func (s *CanvasService) GetCanvas(ctx context.Context, userID int64, canvasID int64) (*CanvasDocument, error) {
	if err := validateCanvasUser(userID); err != nil {
		return nil, err
	}
	if canvasID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CANVAS_ID", "invalid canvas id")
	}
	doc, err := s.repo.GetCanvas(ctx, userID, canvasID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CANVAS_NOT_FOUND", "canvas not found")
	}
	if err != nil {
		return nil, err
	}
	normalizeCanvasDocumentJSON(doc)
	return doc, nil
}

func (s *CanvasService) SaveCanvas(ctx context.Context, userID int64, input CanvasSaveInput) (*CanvasDocument, error) {
	if err := validateCanvasUser(userID); err != nil {
		return nil, err
	}
	normalized, err := normalizeAndValidateCanvasSaveInput(input)
	if err != nil {
		return nil, err
	}
	doc, err := s.repo.SaveCanvas(ctx, userID, normalized)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CANVAS_NOT_FOUND", "canvas not found")
	}
	if err != nil {
		return nil, err
	}
	normalizeCanvasDocumentJSON(doc)
	return doc, nil
}

func (s *CanvasService) DeleteCanvas(ctx context.Context, userID int64, canvasID int64) error {
	if err := validateCanvasUser(userID); err != nil {
		return err
	}
	if canvasID <= 0 {
		return infraerrors.BadRequest("INVALID_CANVAS_ID", "invalid canvas id")
	}
	if err := s.repo.DeleteCanvas(ctx, userID, canvasID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return infraerrors.NotFound("CANVAS_NOT_FOUND", "canvas not found")
		}
		return err
	}
	return nil
}

func (s *CanvasService) CreateRun(ctx context.Context, userID int64, input CanvasRunCreateInput) (*CanvasRun, error) {
	if err := validateCanvasUser(userID); err != nil {
		return nil, err
	}
	normalized, err := normalizeAndValidateCanvasRunInput(input)
	if err != nil {
		return nil, err
	}
	doc, err := s.GetCanvas(ctx, userID, normalized.CanvasID)
	if err != nil {
		return nil, err
	}
	run, err := s.repo.CreateCanvasRun(ctx, userID, normalized)
	if err != nil {
		return nil, err
	}
	run = s.executeCanvasRun(ctx, userID, run, doc, normalized)
	normalizeCanvasRunJSON(run)
	return run, nil
}

func (s *CanvasService) ListRuns(ctx context.Context, userID int64, filters CanvasRunListFilters) ([]CanvasRun, int, error) {
	if err := validateCanvasUser(userID); err != nil {
		return nil, 0, err
	}
	filters = normalizeCanvasRunListFilters(filters)
	runs, total, err := s.repo.ListCanvasRuns(ctx, userID, filters)
	if err != nil {
		return nil, 0, err
	}
	for i := range runs {
		normalizeCanvasRunJSON(&runs[i])
	}
	return runs, total, nil
}

func (s *CanvasService) executeCanvasRun(ctx context.Context, userID int64, run *CanvasRun, doc *CanvasDocument, input CanvasRunCreateInput) *CanvasRun {
	if run == nil || doc == nil || s == nil || s.imageCreator == nil {
		return run
	}
	running, err := s.repo.MarkCanvasRunRunning(ctx, userID, run.ID)
	if err == nil && running != nil {
		run = running
	}
	output, err := s.queueCanvasImageTasks(ctx, userID, doc, input)
	status := CanvasRunStatusSucceeded
	message := ""
	if err != nil {
		status = CanvasRunStatusFailed
		message = imageCreatorTaskErrorMessage(err)
		output["failed"] = true
		output["error_message"] = message
	}
	completed, completeErr := s.repo.CompleteCanvasRun(ctx, userID, run.ID, CanvasRunCompleteInput{
		Status:       status,
		Output:       output,
		ErrorMessage: message,
	})
	if completeErr != nil {
		if err != nil {
			run.Status = CanvasRunStatusFailed
			run.ErrorMessage = message
			run.Output = output
		}
		return run
	}
	return completed
}

func (s *CanvasService) queueCanvasImageTasks(ctx context.Context, userID int64, doc *CanvasDocument, input CanvasRunCreateInput) (map[string]any, error) {
	output := map[string]any{
		"mode":        "image_creator_tasks",
		"canvas_id":   doc.ID,
		"image_tasks": []map[string]any{},
	}
	executableNodes := canvasExecutableImageNodes(doc.Nodes)
	if len(executableNodes) == 0 {
		output["message"] = "no executable image nodes"
		return output, nil
	}
	if input.APIKeyID <= 0 {
		return output, infraerrors.BadRequest("CANVAS_API_KEY_REQUIRED", "api_key_id is required to run image nodes")
	}
	tasks := make([]map[string]any, 0, len(executableNodes))
	for _, node := range executableNodes {
		taskInput, err := s.canvasNodeImageTaskInput(ctx, userID, doc, node, input)
		if err != nil {
			output["image_tasks"] = tasks
			output["failed_node_id"] = node.ID
			return output, err
		}
		task, err := s.imageCreator.CreateTask(ctx, userID, taskInput)
		if err != nil {
			output["image_tasks"] = tasks
			output["failed_node_id"] = node.ID
			return output, err
		}
		tasks = append(tasks, map[string]any{
			"node_id":     node.ID,
			"node_type":   node.Type,
			"task_id":     task.ID,
			"task_status": task.Status,
			"model":       task.Model,
			"prompt":      task.Prompt,
		})
	}
	output["image_tasks"] = tasks
	output["task_count"] = len(tasks)
	return output, nil
}

func (s *CanvasService) canvasNodeImageTaskInput(ctx context.Context, userID int64, doc *CanvasDocument, node CanvasNode, runInput CanvasRunCreateInput) (ImageCreatorCreateTaskInput, error) {
	config := canvasNodeConfig(node)
	prompt := firstNonEmptyString(
		canvasString(config, "prompt"),
		canvasString(config, "text"),
		canvasString(node.Data, "prompt"),
		canvasString(node.Data, "text"),
		canvasUpstreamPrompt(doc, node.ID),
	)
	if strings.TrimSpace(prompt) == "" {
		return ImageCreatorCreateTaskInput{}, infraerrors.BadRequest("CANVAS_NODE_PROMPT_REQUIRED", "image node prompt is required")
	}
	apiKeyID := firstPositiveInt64(canvasInt64(config, "apiKeyId"), canvasInt64(config, "api_key_id"), runInput.APIKeyID)
	input := ImageCreatorCreateTaskInput{
		APIKeyID:     apiKeyID,
		Model:        firstNonEmptyString(canvasString(config, "model"), runInput.Model),
		Prompt:       prompt,
		Size:         canvasString(config, "size"),
		Quality:      canvasString(config, "quality"),
		Count:        int(firstPositiveInt64(canvasInt64(config, "count"), 1)),
		OutputFormat: canvasString(config, "outputFormat"),
		Background:   canvasString(config, "background"),
	}
	dimensionMode := strings.ToLower(strings.TrimSpace(canvasString(config, "dimensionMode")))
	if strings.TrimSpace(input.Size) == "" && (dimensionMode == "ratio" || canvasString(config, "resolution") != "" || canvasString(config, "aspectRatio") != "") {
		input.Size = imageCreatorSizeForResolutionAndAspectRatio(canvasString(config, "resolution"), canvasString(config, "aspectRatio"))
	}
	if input.OutputFormat == "" {
		input.OutputFormat = canvasString(config, "output_format")
	}
	if input.OutputFormat == "" {
		input.OutputFormat = "webp"
	}
	if node.Type != CanvasNodeTypeImageToImage {
		return input, nil
	}
	referenceImageID := firstPositiveInt64(
		canvasInt64(config, "referenceImageId"),
		canvasInt64(config, "reference_image_id"),
		canvasInt64(config, "imageId"),
		canvasInt64(config, "image_id"),
		canvasUpstreamReferenceImageID(doc, node.ID),
	)
	if referenceImageID <= 0 {
		return ImageCreatorCreateTaskInput{}, infraerrors.BadRequest("CANVAS_REFERENCE_IMAGE_REQUIRED", "image_to_image node reference image is required")
	}
	file, err := s.imageCreator.GetReferenceImageForUser(ctx, userID, referenceImageID)
	if err != nil {
		return ImageCreatorCreateTaskInput{}, err
	}
	data, err := readCanvasReferenceImage(file)
	if err != nil {
		return ImageCreatorCreateTaskInput{}, err
	}
	input.ReferenceImage = data
	input.ReferenceImageMimeType = file.ContentType
	input.ReferenceImageFilename = file.FileName
	return input, nil
}

func canvasExecutableImageNodes(nodes []CanvasNode) []CanvasNode {
	out := make([]CanvasNode, 0)
	for _, node := range nodes {
		if node.Type == CanvasNodeTypeTextToImage || node.Type == CanvasNodeTypeImageToImage {
			out = append(out, node)
		}
	}
	return out
}

func canvasNodeConfig(node CanvasNode) map[string]any {
	if config, ok := node.Data["config"].(map[string]any); ok {
		return normalizeCanvasJSONMap(config)
	}
	return map[string]any{}
}

func canvasUpstreamPrompt(doc *CanvasDocument, nodeID string) string {
	if doc == nil {
		return ""
	}
	parts := make([]string, 0)
	for _, edge := range doc.Edges {
		if edge.Target != nodeID {
			continue
		}
		if source := canvasFindNode(doc.Nodes, edge.Source); source != nil {
			if text := canvasNodeText(*source); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func canvasUpstreamReferenceImageID(doc *CanvasDocument, nodeID string) int64 {
	if doc == nil {
		return 0
	}
	for _, edge := range doc.Edges {
		if edge.Target != nodeID {
			continue
		}
		source := canvasFindNode(doc.Nodes, edge.Source)
		if source == nil || source.Type != CanvasNodeTypeImage {
			continue
		}
		config := canvasNodeConfig(*source)
		if id := firstPositiveInt64(
			canvasInt64(config, "referenceImageId"),
			canvasInt64(config, "reference_image_id"),
			canvasInt64(config, "imageId"),
			canvasInt64(config, "image_id"),
		); id > 0 {
			return id
		}
	}
	return 0
}

func canvasFindNode(nodes []CanvasNode, id string) *CanvasNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func canvasNodeText(node CanvasNode) string {
	config := canvasNodeConfig(node)
	return firstNonEmptyString(
		canvasString(config, "prompt"),
		canvasString(config, "text"),
		canvasString(node.Data, "prompt"),
		canvasString(node.Data, "text"),
		canvasString(node.Data, "title"),
	)
}

func canvasString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func canvasInt64(values map[string]any, key string) int64 {
	if len(values) == 0 {
		return 0
	}
	switch value := values[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return n
	default:
		return 0
	}
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func readCanvasReferenceImage(file *ImageCreatorFile) ([]byte, error) {
	if file == nil {
		return nil, infraerrors.NotFound("IMAGE_CREATOR_IMAGE_NOT_FOUND", "image not found")
	}
	var reader io.ReadCloser
	var err error
	switch {
	case file.Body != nil:
		reader = file.Body
	case strings.TrimSpace(file.Path) != "":
		reader, err = os.Open(file.Path)
	default:
		return nil, infraerrors.NotFound("IMAGE_CREATOR_IMAGE_NOT_FOUND", "image file not found")
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, imageCreatorMaxStoredImageBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > imageCreatorMaxStoredImageBytes {
		return nil, infraerrors.BadRequest("REFERENCE_IMAGE_TOO_LARGE", "reference image is too large")
	}
	return data, nil
}

func (s *CanvasService) GetRun(ctx context.Context, userID int64, runID int64) (*CanvasRun, error) {
	if err := validateCanvasUser(userID); err != nil {
		return nil, err
	}
	if runID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CANVAS_RUN_ID", "invalid canvas run id")
	}
	run, err := s.repo.GetCanvasRun(ctx, userID, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CANVAS_RUN_NOT_FOUND", "canvas run not found")
	}
	if err != nil {
		return nil, err
	}
	normalizeCanvasRunJSON(run)
	return run, nil
}

func (s *CanvasService) CancelRun(ctx context.Context, userID int64, runID int64) (*CanvasRun, error) {
	if err := validateCanvasUser(userID); err != nil {
		return nil, err
	}
	if runID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CANVAS_RUN_ID", "invalid canvas run id")
	}
	existing, err := s.repo.GetCanvasRun(ctx, userID, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CANVAS_RUN_NOT_FOUND", "canvas run not found")
	}
	if err != nil {
		return nil, err
	}
	if canceler, ok := s.imageCreator.(CanvasImageTaskCanceler); ok {
		if _, err := canceler.CancelTasksForUser(ctx, userID, canvasRunImageTaskIDs(existing)); err != nil {
			return nil, err
		}
	}
	run, err := s.repo.CancelCanvasRun(ctx, userID, runID)
	if err == nil {
		normalizeCanvasRunJSON(run)
		return run, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	existing, getErr := s.repo.GetCanvasRun(ctx, userID, runID)
	if errors.Is(getErr, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CANVAS_RUN_NOT_FOUND", "canvas run not found")
	}
	if getErr != nil {
		return nil, getErr
	}
	if existing.Status == CanvasRunStatusCanceled {
		normalizeCanvasRunJSON(existing)
		return existing, nil
	}
	return nil, infraerrors.Conflict("CANVAS_RUN_NOT_CANCELABLE", "canvas run is not cancelable")
}

func canvasRunImageTaskIDs(run *CanvasRun) []int64 {
	if run == nil || run.Output == nil {
		return nil
	}
	rawTasks, ok := run.Output["image_tasks"].([]any)
	if !ok {
		return nil
	}
	ids := make([]int64, 0, len(rawTasks))
	for _, rawTask := range rawTasks {
		task, ok := rawTask.(map[string]any)
		if !ok {
			continue
		}
		if id := canvasInt64(task, "task_id"); id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *CanvasService) ListModels(ctx context.Context, userID, apiKeyID int64) (CanvasModelCatalog, error) {
	if err := validateCanvasUser(userID); err != nil {
		return CanvasModelCatalog{}, err
	}
	if apiKeyID <= 0 || s.apiKeys == nil {
		return DefaultCanvasModelCatalog(), nil
	}
	key, err := s.apiKeys.GetByID(ctx, apiKeyID)
	if err != nil || key == nil || key.UserID != userID {
		return CanvasModelCatalog{}, infraerrors.NotFound("API_KEY_NOT_FOUND", "api key not found")
	}
	modelIDs := s.canvasModelsForAPIKey(ctx, key)
	if len(modelIDs) == 0 {
		return DefaultCanvasModelCatalog(), nil
	}
	return canvasModelCatalogForIDs(modelIDs), nil
}

func (s *CanvasService) canvasModelsForAPIKey(ctx context.Context, key *APIKey) []string {
	if key == nil || s.apiKeys == nil || s.apiKeys.groupRepo == nil {
		return nil
	}
	groups := make([]*Group, 0, len(key.GroupRoutes)+1)
	seen := make(map[int64]struct{})
	addGroup := func(group *Group) {
		if group == nil || group.ID <= 0 {
			return
		}
		if _, ok := seen[group.ID]; ok {
			return
		}
		seen[group.ID] = struct{}{}
		groups = append(groups, group)
	}
	addGroup(key.Group)
	if key.GroupID != nil && key.Group == nil {
		if group, err := s.apiKeys.groupRepo.GetByID(ctx, *key.GroupID); err == nil {
			addGroup(group)
		}
	}
	for _, route := range key.GroupRoutes {
		if !route.Enabled {
			continue
		}
		if group, err := s.apiKeys.groupRepo.GetByID(ctx, route.GroupID); err == nil {
			addGroup(group)
		}
	}
	models := make([]string, 0)
	seenModels := make(map[string]struct{})
	addModel := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, ok := seenModels[model]; ok {
			return
		}
		seenModels[model] = struct{}{}
		models = append(models, model)
	}
	for _, group := range groups {
		if !group.ModelAllowlist.Enabled {
			continue
		}
		for _, model := range group.ModelAllowlist.Models {
			addModel(model)
		}
	}
	if s.channelService == nil || len(groups) == 0 {
		return models
	}
	groupIDs := make(map[int64]struct{}, len(groups))
	for _, group := range groups {
		groupIDs[group.ID] = struct{}{}
	}
	channels, err := s.channelService.ListAvailable(ctx)
	if err != nil {
		return models
	}
	for _, channel := range channels {
		if channel.Status != StatusActive {
			continue
		}
		availableToKey := false
		for _, group := range channel.Groups {
			if _, ok := groupIDs[group.ID]; ok {
				availableToKey = true
				break
			}
		}
		if !availableToKey {
			continue
		}
		for _, model := range channel.SupportedModels {
			addModel(model.Name)
		}
	}
	return models
}

func canvasModelCatalogForIDs(modelIDs []string) CanvasModelCatalog {
	models := BuildModelCatalog(PlatformOpenAI, modelIDs)
	defaultChatModel := ""
	if len(models.ChatModels) > 0 {
		defaultChatModel = models.ChatModels[0]
	}
	defaultImageModel := ""
	if len(models.ImageModels) > 0 {
		defaultImageModel = models.ImageModels[0]
	}
	return CanvasModelCatalog{
		Object:            "canvas_model_catalog",
		Items:             models.Items,
		ChatModels:        models.ChatModels,
		ImageModels:       models.ImageModels,
		NodeTypes:         append([]string(nil), canvasNodeTypes...),
		DefaultChatModel:  defaultChatModel,
		DefaultImageModel: defaultImageModel,
	}
}

func DefaultCanvasModelCatalog() CanvasModelCatalog {
	models := BuildModelCatalog(PlatformOpenAI, []string{
		defaultCanvasChatModel,
		"gpt-5.5",
		"gpt-5.6",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		defaultCanvasImageModel,
		"gpt-image-1.5",
	})
	return CanvasModelCatalog{
		Object:            "canvas_model_catalog",
		Items:             models.Items,
		ChatModels:        models.ChatModels,
		ImageModels:       models.ImageModels,
		NodeTypes:         append([]string(nil), canvasNodeTypes...),
		DefaultChatModel:  defaultCanvasChatModel,
		DefaultImageModel: defaultCanvasImageModel,
	}
}

func normalizeAndValidateCanvasSaveInput(input CanvasSaveInput) (CanvasSaveInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		input.Title = defaultCanvasTitle
	}
	if len(input.Title) > maxCanvasTitleLength {
		return input, infraerrors.BadRequest("CANVAS_TITLE_TOO_LONG", "canvas title is too long")
	}
	input.Description = strings.TrimSpace(input.Description)
	if len(input.Description) > maxCanvasDescriptionLength {
		return input, infraerrors.BadRequest("CANVAS_DESCRIPTION_TOO_LONG", "canvas description is too long")
	}
	input.Viewport = normalizeCanvasJSONMap(input.Viewport)
	input.Metadata = normalizeCanvasJSONMap(input.Metadata)
	if err := validateCanvasJSON("viewport", input.Viewport); err != nil {
		return input, err
	}
	if err := validateCanvasJSON("metadata", input.Metadata); err != nil {
		return input, err
	}
	if len(input.Nodes) > maxCanvasNodeCount {
		return input, infraerrors.BadRequest("CANVAS_TOO_MANY_NODES", fmt.Sprintf("canvas cannot contain more than %d nodes", maxCanvasNodeCount))
	}
	if len(input.Edges) > maxCanvasEdgeCount {
		return input, infraerrors.BadRequest("CANVAS_TOO_MANY_EDGES", fmt.Sprintf("canvas cannot contain more than %d edges", maxCanvasEdgeCount))
	}
	nodeIDs := make(map[string]struct{}, len(input.Nodes))
	for i := range input.Nodes {
		node, err := normalizeAndValidateCanvasNode(input.Nodes[i], nodeIDs)
		if err != nil {
			return input, err
		}
		input.Nodes[i] = node
		nodeIDs[node.ID] = struct{}{}
	}
	edgeIDs := make(map[string]struct{}, len(input.Edges))
	for i := range input.Edges {
		edge, err := normalizeAndValidateCanvasEdge(input.Edges[i], edgeIDs, nodeIDs)
		if err != nil {
			return input, err
		}
		input.Edges[i] = edge
		edgeIDs[edge.ID] = struct{}{}
	}
	return input, nil
}

func normalizeAndValidateCanvasNode(node CanvasNode, seen map[string]struct{}) (CanvasNode, error) {
	node.ID = strings.TrimSpace(node.ID)
	if node.ID == "" {
		return node, infraerrors.BadRequest("CANVAS_NODE_ID_REQUIRED", "canvas node id is required")
	}
	if _, ok := seen[node.ID]; ok {
		return node, infraerrors.BadRequest("CANVAS_NODE_ID_DUPLICATED", "canvas node id is duplicated")
	}
	node.Type = strings.ToLower(strings.TrimSpace(node.Type))
	if _, ok := canvasNodeTypeSet[node.Type]; !ok {
		return node, infraerrors.BadRequest("CANVAS_NODE_TYPE_INVALID", "canvas node type is invalid")
	}
	node.Position = normalizeCanvasJSONMap(node.Position)
	node.Data = normalizeCanvasJSONMap(node.Data)
	node.Metadata = normalizeCanvasJSONMap(node.Metadata)
	if err := validateCanvasJSON("node position", node.Position); err != nil {
		return node, err
	}
	if err := validateCanvasJSON("node data", node.Data); err != nil {
		return node, err
	}
	if err := validateCanvasJSON("node metadata", node.Metadata); err != nil {
		return node, err
	}
	return node, nil
}

func normalizeAndValidateCanvasEdge(edge CanvasEdge, seen map[string]struct{}, nodes map[string]struct{}) (CanvasEdge, error) {
	edge.ID = strings.TrimSpace(edge.ID)
	if edge.ID == "" {
		return edge, infraerrors.BadRequest("CANVAS_EDGE_ID_REQUIRED", "canvas edge id is required")
	}
	if _, ok := seen[edge.ID]; ok {
		return edge, infraerrors.BadRequest("CANVAS_EDGE_ID_DUPLICATED", "canvas edge id is duplicated")
	}
	edge.Source = strings.TrimSpace(edge.Source)
	edge.Target = strings.TrimSpace(edge.Target)
	edge.SourceHandle = strings.TrimSpace(edge.SourceHandle)
	edge.TargetHandle = strings.TrimSpace(edge.TargetHandle)
	if edge.Source == "" || edge.Target == "" {
		return edge, infraerrors.BadRequest("CANVAS_EDGE_ENDPOINT_REQUIRED", "canvas edge source and target are required")
	}
	if _, ok := nodes[edge.Source]; !ok {
		return edge, infraerrors.BadRequest("CANVAS_EDGE_SOURCE_INVALID", "canvas edge source node does not exist")
	}
	if _, ok := nodes[edge.Target]; !ok {
		return edge, infraerrors.BadRequest("CANVAS_EDGE_TARGET_INVALID", "canvas edge target node does not exist")
	}
	edge.Data = normalizeCanvasJSONMap(edge.Data)
	edge.Metadata = normalizeCanvasJSONMap(edge.Metadata)
	if err := validateCanvasJSON("edge data", edge.Data); err != nil {
		return edge, err
	}
	if err := validateCanvasJSON("edge metadata", edge.Metadata); err != nil {
		return edge, err
	}
	return edge, nil
}

func normalizeAndValidateCanvasRunInput(input CanvasRunCreateInput) (CanvasRunCreateInput, error) {
	if input.CanvasID <= 0 {
		return input, infraerrors.BadRequest("CANVAS_ID_REQUIRED", "canvas id is required")
	}
	if input.APIKeyID < 0 {
		return input, infraerrors.BadRequest("INVALID_API_KEY", "invalid api key id")
	}
	input.Model = strings.TrimSpace(input.Model)
	if input.Model == "" {
		input.Model = defaultCanvasImageModel
	}
	input.TriggerType = strings.TrimSpace(input.TriggerType)
	if input.TriggerType == "" {
		input.TriggerType = defaultCanvasRunTriggerType
	}
	input.Input = normalizeCanvasJSONMap(input.Input)
	input.Metadata = normalizeCanvasJSONMap(input.Metadata)
	if err := validateCanvasJSON("run input", input.Input); err != nil {
		return input, err
	}
	if err := validateCanvasJSON("run metadata", input.Metadata); err != nil {
		return input, err
	}
	return input, nil
}

func normalizeCanvasListFilters(filters CanvasListFilters) CanvasListFilters {
	if filters.Limit <= 0 {
		filters.Limit = 20
	}
	if filters.Limit > 100 {
		filters.Limit = 100
	}
	if filters.Offset < 0 {
		filters.Offset = 0
	}
	return filters
}

func normalizeCanvasRunListFilters(filters CanvasRunListFilters) CanvasRunListFilters {
	if filters.Limit <= 0 {
		filters.Limit = 20
	}
	if filters.Limit > 100 {
		filters.Limit = 100
	}
	if filters.Offset < 0 {
		filters.Offset = 0
	}
	return filters
}

func normalizeCanvasJSONMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = item
	}
	if len(out) == 0 {
		return map[string]any{}
	}
	return out
}

func validateCanvasJSON(name string, value map[string]any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return infraerrors.BadRequest("CANVAS_JSON_INVALID", name+" must be valid JSON")
	}
	if len(data) > maxCanvasJSONBytes {
		return infraerrors.BadRequest("CANVAS_JSON_TOO_LARGE", name+" is too large")
	}
	return nil
}

func validateCanvasUser(userID int64) error {
	if userID <= 0 {
		return infraerrors.Unauthorized("UNAUTHORIZED", "authentication required")
	}
	return nil
}

func normalizeCanvasDocumentJSON(doc *CanvasDocument) {
	if doc == nil {
		return
	}
	if doc.Nodes == nil {
		doc.Nodes = []CanvasNode{}
	}
	if doc.Edges == nil {
		doc.Edges = []CanvasEdge{}
	}
	doc.Viewport = normalizeCanvasJSONMap(doc.Viewport)
	doc.Metadata = normalizeCanvasJSONMap(doc.Metadata)
	for i := range doc.Nodes {
		doc.Nodes[i].Position = normalizeCanvasJSONMap(doc.Nodes[i].Position)
		doc.Nodes[i].Data = normalizeCanvasJSONMap(doc.Nodes[i].Data)
		doc.Nodes[i].Metadata = normalizeCanvasJSONMap(doc.Nodes[i].Metadata)
	}
	for i := range doc.Edges {
		doc.Edges[i].Data = normalizeCanvasJSONMap(doc.Edges[i].Data)
		doc.Edges[i].Metadata = normalizeCanvasJSONMap(doc.Edges[i].Metadata)
	}
}

func normalizeCanvasSummaryJSON(item *CanvasListItem) {
	if item == nil {
		return
	}
	item.Viewport = normalizeCanvasJSONMap(item.Viewport)
	item.Metadata = normalizeCanvasJSONMap(item.Metadata)
}

func normalizeCanvasRunJSON(run *CanvasRun) {
	if run == nil {
		return
	}
	run.Input = normalizeCanvasJSONMap(run.Input)
	run.Output = normalizeCanvasJSONMap(run.Output)
	run.Metadata = normalizeCanvasJSONMap(run.Metadata)
}
