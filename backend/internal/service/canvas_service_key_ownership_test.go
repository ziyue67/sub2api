package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type canvasKeyOwnershipRepo struct {
	APIKeyRepository
	owned map[int64]bool
}

func (r *canvasKeyOwnershipRepo) VerifyOwnership(_ context.Context, _ int64, ids []int64) ([]int64, error) {
	valid := make([]int64, 0, len(ids))
	for _, id := range ids {
		if r.owned[id] {
			valid = append(valid, id)
		}
	}
	return valid, nil
}

type canvasRunPreflightRepo struct {
	CanvasRepository
	doc         *CanvasDocument
	createCalls int
}

func (r *canvasRunPreflightRepo) GetCanvas(context.Context, int64, int64) (*CanvasDocument, error) {
	return r.doc, nil
}

func (r *canvasRunPreflightRepo) CreateCanvasRun(_ context.Context, userID int64, input CanvasRunCreateInput) (*CanvasRun, error) {
	r.createCalls++
	return &CanvasRun{ID: 100, UserID: userID, CanvasID: input.CanvasID, Status: CanvasRunStatusPending}, nil
}

func (r *canvasRunPreflightRepo) MarkCanvasRunRunning(_ context.Context, userID, runID int64) (*CanvasRun, error) {
	return &CanvasRun{ID: runID, UserID: userID, Status: CanvasRunStatusRunning}, nil
}

func (r *canvasRunPreflightRepo) CompleteCanvasRun(_ context.Context, userID, runID int64, input CanvasRunCompleteInput) (*CanvasRun, error) {
	return &CanvasRun{ID: runID, UserID: userID, Status: input.Status, Output: input.Output, ErrorMessage: input.ErrorMessage}, nil
}

type canvasRunPreflightImageCreator struct {
	createCalls int
}

func (c *canvasRunPreflightImageCreator) CreateTask(_ context.Context, userID int64, input ImageCreatorCreateTaskInput) (*ImageCreatorTask, error) {
	c.createCalls++
	return &ImageCreatorTask{ID: int64(c.createCalls), UserID: userID, APIKeyID: input.APIKeyID, Status: ImageCreatorTaskStatusPending}, nil
}

func (c *canvasRunPreflightImageCreator) GetReferenceImageForUser(context.Context, int64, int64) (*ImageCreatorFile, error) {
	panic("unexpected reference image lookup")
}

func newCanvasRunPreflightService(doc *CanvasDocument, ownedKeys map[int64]bool) (*CanvasService, *canvasRunPreflightRepo, *canvasRunPreflightImageCreator) {
	repo := &canvasRunPreflightRepo{doc: doc}
	creator := &canvasRunPreflightImageCreator{}
	keyService := NewAPIKeyService(&canvasKeyOwnershipRepo{owned: ownedKeys}, nil, nil, nil, nil, nil, &config.Config{})
	return NewCanvasServiceWithDeps(repo, creator, keyService), repo, creator
}

func canvasTextToImageNode(id string, apiKeyID int64) CanvasNode {
	config := map[string]any{"prompt": "test prompt"}
	if apiKeyID > 0 {
		config["apiKeyId"] = apiKeyID
	}
	return CanvasNode{ID: id, Type: CanvasNodeTypeTextToImage, Data: map[string]any{"config": config}}
}

func TestCanvasCreateRunRejectsUnownedRequestKeyBeforePersistingRun(t *testing.T) {
	svc, repo, creator := newCanvasRunPreflightService(&CanvasDocument{
		ID:    7,
		Nodes: []CanvasNode{canvasTextToImageNode("image-1", 0)},
	}, map[int64]bool{})

	run, err := svc.CreateRun(context.Background(), 42, CanvasRunCreateInput{CanvasID: 7, APIKeyID: 91})

	require.Error(t, err)
	require.Nil(t, run)
	require.Zero(t, repo.createCalls, "an invalid request key must not leave a canvas run record")
	require.Zero(t, creator.createCalls, "an invalid request key must not create an image task")
}

func TestCanvasCreateRunPreflightsAllNodeOverrideKeysBeforeCreatingAnyTask(t *testing.T) {
	svc, repo, creator := newCanvasRunPreflightService(&CanvasDocument{
		ID: 7,
		Nodes: []CanvasNode{
			canvasTextToImageNode("valid-first", 101),
			canvasTextToImageNode("invalid-second", 202),
		},
	}, map[int64]bool{101: true})

	run, err := svc.CreateRun(context.Background(), 42, CanvasRunCreateInput{CanvasID: 7, APIKeyID: 101})

	require.Error(t, err)
	require.Nil(t, run)
	require.Zero(t, repo.createCalls, "all effective keys must be valid before the run is persisted")
	require.Zero(t, creator.createCalls, "a later invalid override must not allow earlier nodes to create tasks")
}

func TestCanvasCreateRunCreatesTasksAfterAllEffectiveKeysPassOwnershipPreflight(t *testing.T) {
	svc, repo, creator := newCanvasRunPreflightService(&CanvasDocument{
		ID: 7,
		Nodes: []CanvasNode{
			canvasTextToImageNode("request-key", 0),
			canvasTextToImageNode("override-key", 202),
		},
	}, map[int64]bool{101: true, 202: true})

	run, err := svc.CreateRun(context.Background(), 42, CanvasRunCreateInput{CanvasID: 7, APIKeyID: 101})

	require.NoError(t, err)
	require.NotNil(t, run)
	require.Equal(t, CanvasRunStatusSucceeded, run.Status)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 2, creator.createCalls)
}
