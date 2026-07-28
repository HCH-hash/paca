package e2e_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// createWorkflowViaAPI creates a draft workflow via the API and returns its ID.
func createWorkflowViaAPI(t *testing.T, env *e2eEnv, client *http.Client, token, projectID, name string) string {
	t.Helper()
	body := jsonBody(t, map[string]any{"name": name, "description": "e2e workflow"})
	req := mustRequest(env.ctx, t, http.MethodPost,
		fmt.Sprintf("%s/api/v1/projects/%s/workflows", env.base, projectID), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp := mustDo(t, client, req)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusCreated)
	var env2 envelope
	decodeJSON(t, resp, &env2)
	data := assertDataMap(t, env2)
	id, _ := data["id"].(string)
	if id == "" {
		t.Fatal("expected non-empty workflow id")
	}
	return id
}

// getWorkflowGraphViaAPI fetches the full workflow graph: workflow metadata
// plus its nodes, edges, status rules, and status transitions.
func getWorkflowGraphViaAPI(t *testing.T, env *e2eEnv, client *http.Client, token, projectID, workflowID string) map[string]any {
	t.Helper()
	req := mustRequest(env.ctx, t, http.MethodGet,
		fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projectID, workflowID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := mustDo(t, client, req)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusOK)
	var env2 envelope
	decodeJSON(t, resp, &env2)
	return assertDataMap(t, env2)
}

// listProjectMembersViaAPI returns all members of a project as decoded maps.
func listProjectMembersViaAPI(t *testing.T, env *e2eEnv, client *http.Client, token, projectID string) []map[string]any {
	t.Helper()
	req := mustRequest(env.ctx, t, http.MethodGet,
		fmt.Sprintf("%s/api/v1/projects/%s/members", env.base, projectID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := mustDo(t, client, req)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusOK)
	var env2 envelope
	decodeJSON(t, resp, &env2)
	raw, ok := env2.Data.([]any)
	if !ok {
		t.Fatalf("expected members array, got %T", env2.Data)
	}
	members := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			members = append(members, m)
		}
	}
	return members
}

// memberIDForUser returns the project_members.id of the member whose user_id
// matches userID, or "" if no such member is present.
func memberIDForUser(members []map[string]any, userID string) string {
	for _, m := range members {
		if uid, _ := m["user_id"].(string); uid == userID {
			id, _ := m["id"].(string)
			return id
		}
	}
	return ""
}

// listTaskStatusesViaAPI returns all task statuses for a project as decoded maps.
func listTaskStatusesViaAPI(t *testing.T, env *e2eEnv, client *http.Client, token, projectID string) []map[string]any {
	t.Helper()
	req := mustRequest(env.ctx, t, http.MethodGet,
		fmt.Sprintf("%s/api/v1/projects/%s/task-statuses", env.base, projectID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := mustDo(t, client, req)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusOK)
	var env2 envelope
	decodeJSON(t, resp, &env2)
	data := assertDataMap(t, env2)
	items, _ := data["items"].([]any)
	statuses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			statuses = append(statuses, m)
		}
	}
	return statuses
}

// statusIDByName returns the id of the task status with the given name, or
// "" if not found.
func statusIDByName(statuses []map[string]any, name string) string {
	for _, s := range statuses {
		if n, _ := s["name"].(string); n == name {
			id, _ := s["id"].(string)
			return id
		}
	}
	return ""
}

// addWorkflowNodeViaAPI adds taskID as a node in workflowID and returns the
// decoded node response.
func addWorkflowNodeViaAPI(t *testing.T, env *e2eEnv, client *http.Client, token, projectID, workflowID, taskID string) map[string]any {
	t.Helper()
	body := jsonBody(t, map[string]any{"task_id": taskID, "pos_x": 10.0, "pos_y": 20.0})
	req := mustRequest(env.ctx, t, http.MethodPost,
		fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes", env.base, projectID, workflowID), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp := mustDo(t, client, req)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusCreated)
	var env2 envelope
	decodeJSON(t, resp, &env2)
	return assertDataMap(t, env2)
}

// addWorkflowEdgeViaAPI links sourceNodeID -> targetNodeID and returns the
// created edge's id.
func addWorkflowEdgeViaAPI(t *testing.T, env *e2eEnv, client *http.Client, token, projectID, workflowID, sourceNodeID, targetNodeID string) string {
	t.Helper()
	body := jsonBody(t, map[string]any{"source_node_id": sourceNodeID, "target_node_id": targetNodeID})
	req := mustRequest(env.ctx, t, http.MethodPost,
		fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/edges", env.base, projectID, workflowID), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp := mustDo(t, client, req)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusCreated)
	var env2 envelope
	decodeJSON(t, resp, &env2)
	data := assertDataMap(t, env2)
	id, _ := data["id"].(string)
	return id
}

// activateWorkflowViaAPI activates a draft workflow and returns the decoded
// workflow response.
func activateWorkflowViaAPI(t *testing.T, env *e2eEnv, client *http.Client, token, projectID, workflowID string) map[string]any {
	t.Helper()
	req := mustRequest(env.ctx, t, http.MethodPost,
		fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/activate", env.base, projectID, workflowID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := mustDo(t, client, req)
	defer func() { _ = resp.Body.Close() }()
	assertStatus(t, resp, http.StatusOK)
	var env2 envelope
	decodeJSON(t, resp, &env2)
	return assertDataMap(t, env2)
}

// ---------------------------------------------------------------------------
// Workflow CRUD + auto-seeded defaults
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_CRUD(t *testing.T) {
	env := newE2EEnv(t)
	username := "workflow-crud-user-" + uuid.NewString()
	seedTaskMemberUser(t, env, username, "workflowpass1")
	client, token := taskMemberLogin(t, env, username, "workflowpass1")
	projID := createProjectForTasksViaAPI(t, env, client, token)

	user, err := env.userRepo.FindByUsername(env.ctx, username)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	members := listProjectMembersViaAPI(t, env, client, token, projID)
	creatorMemberID := memberIDForUser(members, user.ID.String())
	if creatorMemberID == "" {
		t.Fatal("expected the project creator to already be a project member")
	}

	statuses := listTaskStatusesViaAPI(t, env, client, token, projID)
	if len(statuses) != 4 {
		t.Fatalf("expected 4 default task statuses, got %d", len(statuses))
	}
	doneStatusID := statusIDByName(statuses, "Done")

	var workflowID string

	t.Run("create_workflow", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"name": "Bug Triage", "description": "Automates bug triage"})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows", env.base, projID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusCreated)

		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		workflowID, _ = data["id"].(string)
		if workflowID == "" {
			t.Fatal("expected non-empty workflow id")
		}
		if name, _ := data["name"].(string); name != "Bug Triage" {
			t.Errorf("expected name %q, got %q", "Bug Triage", name)
		}
		if status, _ := data["status"].(string); status != "draft" {
			t.Errorf("expected status draft, got %q", status)
		}
		if pid, _ := data["project_id"].(string); pid != projID {
			t.Errorf("expected project_id %q, got %q", projID, pid)
		}
		if createdBy, _ := data["created_by"].(string); createdBy != creatorMemberID {
			t.Errorf("expected created_by %q, got %q", creatorMemberID, createdBy)
		}
	})

	t.Run("create_seeds_default_status_transitions_and_rules", func(t *testing.T) {
		graph := getWorkflowGraphViaAPI(t, env, client, token, projID, workflowID)

		nodes, _ := graph["nodes"].([]any)
		if len(nodes) != 0 {
			t.Errorf("expected 0 nodes on a freshly created workflow, got %d", len(nodes))
		}
		edges, _ := graph["edges"].([]any)
		if len(edges) != 0 {
			t.Errorf("expected 0 edges on a freshly created workflow, got %d", len(edges))
		}

		transitions, _ := graph["status_transitions"].([]any)
		if len(transitions) != 4 {
			t.Fatalf("expected 4 auto-seeded status transitions, got %d", len(transitions))
		}
		terminalCount := 0
		for _, item := range transitions {
			tr, _ := item.(map[string]any)
			if tr["next_status_id"] == nil {
				terminalCount++
				if statusID, _ := tr["status_id"].(string); statusID != doneStatusID {
					t.Errorf("expected the terminal status to be %q (Done), got %q", doneStatusID, statusID)
				}
			}
		}
		if terminalCount != 1 {
			t.Errorf("expected exactly 1 terminal status transition, got %d", terminalCount)
		}

		rules, _ := graph["status_rules"].([]any)
		if len(rules) != 4 {
			t.Fatalf("expected 4 auto-seeded status rules (one per status), got %d", len(rules))
		}
		for _, item := range rules {
			r, _ := item.(map[string]any)
			if assignee, _ := r["assignee_member_id"].(string); assignee != creatorMemberID {
				t.Errorf("expected default assignee %q, got %q", creatorMemberID, assignee)
			}
		}
	})

	t.Run("list_workflows", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows", env.base, projID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		items, _ := data["items"].([]any)
		found := false
		for _, item := range items {
			m, _ := item.(map[string]any)
			if id, _ := m["id"].(string); id == workflowID {
				found = true
			}
		}
		if !found {
			t.Error("expected created workflow to appear in the list")
		}
	})

	t.Run("list_workflows_filter_by_status", func(t *testing.T) {
		// The workflow is still draft, so filtering for draft must include it
		// and filtering for active must exclude it.
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows?status=draft", env.base, projID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		items, _ := data["items"].([]any)
		found := false
		for _, item := range items {
			m, _ := item.(map[string]any)
			if id, _ := m["id"].(string); id == workflowID {
				found = true
			}
		}
		if !found {
			t.Error("expected draft workflow to appear in status=draft filter")
		}

		req2 := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows?status=active", env.base, projID), nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2 := mustDo(t, client, req2)
		defer func() { _ = resp2.Body.Close() }()
		assertStatus(t, resp2, http.StatusOK)
		var env3 envelope
		decodeJSON(t, resp2, &env3)
		data2 := assertDataMap(t, env3)
		items2, _ := data2["items"].([]any)
		for _, item := range items2 {
			m, _ := item.(map[string]any)
			if id, _ := m["id"].(string); id == workflowID {
				t.Error("draft workflow should not appear in status=active filter")
			}
		}
	})

	t.Run("update_workflow_name_and_description", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"name": "Bug Triage Renamed", "description": "Updated description"})
		req := mustRequest(env.ctx, t, http.MethodPatch,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		if name, _ := data["name"].(string); name != "Bug Triage Renamed" {
			t.Errorf("expected updated name, got %q", name)
		}
		if desc, _ := data["description"].(string); desc != "Updated description" {
			t.Errorf("expected updated description, got %q", desc)
		}
	})

	t.Run("delete_workflow_then_get_not_found", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNoContent)

		req2 := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, workflowID), nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2 := mustDo(t, client, req2)
		defer func() { _ = resp2.Body.Close() }()
		assertStatus(t, resp2, http.StatusNotFound)
		assertErrorCode(t, resp2, "WORKFLOW_NOT_FOUND")
	})
}

// ---------------------------------------------------------------------------
// Nodes and edges
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_NodesAndEdges(t *testing.T) {
	env := newE2EEnv(t)
	username := "workflow-nodes-user-" + uuid.NewString()
	seedTaskMemberUser(t, env, username, "workflownodes1")
	client, token := taskMemberLogin(t, env, username, "workflownodes1")
	projID := createProjectForTasksViaAPI(t, env, client, token)

	task1 := createTaskViaAPI(t, env, client, token, projID, "Task 1")
	task2 := createTaskViaAPI(t, env, client, token, projID, "Task 2")
	task3 := createTaskViaAPI(t, env, client, token, projID, "Task 3")

	workflowID := createWorkflowViaAPI(t, env, client, token, projID, "Node Graph Workflow")

	var node1ID, node2ID, node3ID string

	t.Run("add_nodes", func(t *testing.T) {
		data := addWorkflowNodeViaAPI(t, env, client, token, projID, workflowID, task1)
		node1ID, _ = data["id"].(string)
		if node1ID == "" {
			t.Fatal("expected non-empty node id")
		}
		if taskID, _ := data["task_id"].(string); taskID != task1 {
			t.Errorf("expected task_id %q, got %q", task1, taskID)
		}
		if posX, _ := data["pos_x"].(float64); posX != 10.0 {
			t.Errorf("expected pos_x 10.0, got %v", posX)
		}

		data2 := addWorkflowNodeViaAPI(t, env, client, token, projID, workflowID, task2)
		node2ID, _ = data2["id"].(string)
		data3 := addWorkflowNodeViaAPI(t, env, client, token, projID, workflowID, task3)
		node3ID, _ = data3["id"].(string)
	})

	t.Run("add_duplicate_node_rejected", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"task_id": task1, "pos_x": 0.0, "pos_y": 0.0})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusConflict)
		assertErrorCode(t, resp, "WORKFLOW_NODE_DUPLICATE_TASK")
	})

	t.Run("add_node_missing_task_id_rejected", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"pos_x": 0.0, "pos_y": 0.0})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("add_node_task_cross_project_rejected", func(t *testing.T) {
		otherProjID := createProjectForTasksViaAPI(t, env, client, token)
		otherTask := createTaskViaAPI(t, env, client, token, otherProjID, "Other project task")
		body := jsonBody(t, map[string]any{"task_id": otherTask, "pos_x": 0.0, "pos_y": 0.0})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_NODE_TASK_CROSS_PROJECT")
	})

	t.Run("update_node_position", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"pos_x": 100.5, "pos_y": 200.5})
		req := mustRequest(env.ctx, t, http.MethodPatch,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes/%s", env.base, projID, workflowID, node1ID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		if posX, _ := data["pos_x"].(float64); posX != 100.5 {
			t.Errorf("expected pos_x 100.5, got %v", posX)
		}
		if posY, _ := data["pos_y"].(float64); posY != 200.5 {
			t.Errorf("expected pos_y 200.5, got %v", posY)
		}
	})

	t.Run("add_edge_node1_to_node2", func(t *testing.T) {
		id := addWorkflowEdgeViaAPI(t, env, client, token, projID, workflowID, node1ID, node2ID)
		if id == "" {
			t.Fatal("expected non-empty edge id")
		}
	})

	var edge2ID string
	t.Run("add_edge_node2_to_node3", func(t *testing.T) {
		edge2ID = addWorkflowEdgeViaAPI(t, env, client, token, projID, workflowID, node2ID, node3ID)
		if edge2ID == "" {
			t.Fatal("expected non-empty edge id")
		}
	})

	t.Run("add_edge_self_loop_rejected", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"source_node_id": node1ID, "target_node_id": node1ID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/edges", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_EDGE_SELF_LOOP")
	})

	t.Run("add_duplicate_edge_rejected", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"source_node_id": node1ID, "target_node_id": node2ID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/edges", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusConflict)
		assertErrorCode(t, resp, "WORKFLOW_EDGE_DUPLICATE")
	})

	t.Run("add_edge_that_would_create_cycle_rejected", func(t *testing.T) {
		// node1 -> node2 -> node3 already exists; node3 -> node1 would close the loop.
		body := jsonBody(t, map[string]any{"source_node_id": node3ID, "target_node_id": node1ID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/edges", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_EDGE_CYCLE")
	})

	t.Run("remove_edge", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/edges/%s", env.base, projID, workflowID, edge2ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNoContent)

		graph := getWorkflowGraphViaAPI(t, env, client, token, projID, workflowID)
		edges, _ := graph["edges"].([]any)
		if len(edges) != 1 {
			t.Errorf("expected 1 edge remaining after removal, got %d", len(edges))
		}
	})

	t.Run("remove_node_cascades_its_edges", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes/%s", env.base, projID, workflowID, node2ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNoContent)

		graph := getWorkflowGraphViaAPI(t, env, client, token, projID, workflowID)
		nodes, _ := graph["nodes"].([]any)
		if len(nodes) != 2 {
			t.Errorf("expected 2 nodes remaining, got %d", len(nodes))
		}
		edges, _ := graph["edges"].([]any)
		if len(edges) != 0 {
			t.Errorf("expected 0 edges remaining after the node-removal cascade, got %d", len(edges))
		}
	})
}

// ---------------------------------------------------------------------------
// Status rules and status transitions ("status workflow")
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_StatusTransitions(t *testing.T) {
	env := newE2EEnv(t)
	ownerUsername := "workflow-transitions-owner-" + uuid.NewString()
	seedTaskMemberUser(t, env, ownerUsername, "workflowtransitions1")
	ownerClient, ownerToken := taskMemberLogin(t, env, ownerUsername, "workflowtransitions1")
	projID := createProjectForTasksViaAPI(t, env, ownerClient, ownerToken)

	statuses := listTaskStatusesViaAPI(t, env, ownerClient, ownerToken, projID)
	backlogID := statusIDByName(statuses, "Backlog")
	todoID := statusIDByName(statuses, "Todo")
	inProgressID := statusIDByName(statuses, "In Progress")
	doneID := statusIDByName(statuses, "Done")

	workflowID := createWorkflowViaAPI(t, env, ownerClient, ownerToken, projID, "Transition Chain")

	t.Run("set_status_transition_updates_auto_seeded_chain_in_place", func(t *testing.T) {
		// Default chain: Backlog->Todo->InProgress->Done(nil). Rewire Backlog to skip Todo.
		body := jsonBody(t, map[string]any{"status_id": backlogID, "next_status_id": inProgressID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/status-transitions", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		resp := mustDo(t, ownerClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusCreated)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		if next, _ := data["next_status_id"].(string); next != inProgressID {
			t.Errorf("expected next_status_id %q, got %q", inProgressID, next)
		}

		graph := getWorkflowGraphViaAPI(t, env, ownerClient, ownerToken, projID, workflowID)
		transitions, _ := graph["status_transitions"].([]any)
		if len(transitions) != 4 {
			t.Errorf("expected transition count to remain 4 (upsert, not insert), got %d", len(transitions))
		}
	})

	t.Run("set_status_transition_self_loop_rejected", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"status_id": todoID, "next_status_id": todoID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/status-transitions", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		resp := mustDo(t, ownerClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_STATUS_TRANSITION_SELF_LOOP")
	})

	t.Run("set_status_transition_cross_project_rejected", func(t *testing.T) {
		otherProjID := createProjectForTasksViaAPI(t, env, ownerClient, ownerToken)
		otherStatuses := listTaskStatusesViaAPI(t, env, ownerClient, ownerToken, otherProjID)
		otherStatusID := statusIDByName(otherStatuses, "Todo")

		body := jsonBody(t, map[string]any{"status_id": doneID, "next_status_id": otherStatusID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/status-transitions", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		resp := mustDo(t, ownerClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_STATUS_TRANSITION_CROSS_PROJECT")
	})

	var terminalTransitionID string
	t.Run("set_status_transition_with_no_next_marks_terminal", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"status_id": inProgressID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/status-transitions", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		resp := mustDo(t, ownerClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusCreated)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		terminalTransitionID, _ = data["id"].(string)
		if next, exists := data["next_status_id"]; exists && next != nil {
			t.Errorf("expected next_status_id to be nil/absent, got %v", next)
		}
	})

	t.Run("remove_status_transition", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/status-transitions/%s", env.base, projID, workflowID, terminalTransitionID), nil)
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		resp := mustDo(t, ownerClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNoContent)

		graph := getWorkflowGraphViaAPI(t, env, ownerClient, ownerToken, projID, workflowID)
		transitions, _ := graph["status_transitions"].([]any)
		if len(transitions) != 3 {
			t.Errorf("expected 3 transitions remaining after removal, got %d", len(transitions))
		}
	})
}

// ---------------------------------------------------------------------------
// Lifecycle: draft -> active -> archived, and revert-to-draft
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_Lifecycle(t *testing.T) {
	env := newE2EEnv(t)
	username := "workflow-lifecycle-user-" + uuid.NewString()
	seedTaskMemberUser(t, env, username, "workflowlife1")
	client, token := taskMemberLogin(t, env, username, "workflowlife1")
	projID := createProjectForTasksViaAPI(t, env, client, token)

	taskA := createTaskViaAPI(t, env, client, token, projID, "Task A")
	workflowID := createWorkflowViaAPI(t, env, client, token, projID, "Lifecycle Workflow")

	t.Run("activate_empty_workflow_rejected", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/activate", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_ACTIVATE_NO_NODES")
	})

	var nodeID string
	t.Run("add_node_then_activate", func(t *testing.T) {
		data := addWorkflowNodeViaAPI(t, env, client, token, projID, workflowID, taskA)
		nodeID, _ = data["id"].(string)

		data2 := activateWorkflowViaAPI(t, env, client, token, projID, workflowID)
		if status, _ := data2["status"].(string); status != "active" {
			t.Errorf("expected status active, got %q", status)
		}
	})

	t.Run("active_workflow_appears_in_status_filter", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows?status=active", env.base, projID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		items, _ := data["items"].([]any)
		found := false
		for _, item := range items {
			m, _ := item.(map[string]any)
			if id, _ := m["id"].(string); id == workflowID {
				found = true
			}
		}
		if !found {
			t.Error("expected active workflow to appear in status=active filter")
		}
	})

	t.Run("node_mutations_allowed_while_active", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"pos_x": 1.0, "pos_y": 1.0})
		req := mustRequest(env.ctx, t, http.MethodPatch,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes/%s", env.base, projID, workflowID, nodeID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
	})

	t.Run("rename_allowed_while_active", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"name": "Renamed While Active"})
		req := mustRequest(env.ctx, t, http.MethodPatch,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
	})

	t.Run("activate_already_active_rejected", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/activate", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusConflict)
		assertErrorCode(t, resp, "WORKFLOW_NOT_DRAFT")
	})

	t.Run("revert_to_draft", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/revert-to-draft", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		if status, _ := data["status"].(string); status != "draft" {
			t.Errorf("expected status draft after revert, got %q", status)
		}
	})

	t.Run("draft_mutations_allowed_again_after_revert", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"pos_x": 5.0, "pos_y": 5.0})
		req := mustRequest(env.ctx, t, http.MethodPatch,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes/%s", env.base, projID, workflowID, nodeID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
	})

	t.Run("archive_requires_active_workflow", func(t *testing.T) {
		// The workflow is currently draft (just reverted); archiving a draft is invalid.
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/archive", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusConflict)
		assertErrorCode(t, resp, "WORKFLOW_NOT_ACTIVE")
	})

	t.Run("reactivate_then_archive_then_revert_rejected", func(t *testing.T) {
		activateWorkflowViaAPI(t, env, client, token, projID, workflowID)

		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/archive", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		if status, _ := data["status"].(string); status != "archived" {
			t.Errorf("expected status archived, got %q", status)
		}

		// Unlike active, an archived workflow's graph is locked again.
		body := jsonBody(t, map[string]any{"pos_x": 2.0, "pos_y": 2.0})
		reqNode := mustRequest(env.ctx, t, http.MethodPatch,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes/%s", env.base, projID, workflowID, nodeID), body)
		reqNode.Header.Set("Content-Type", "application/json")
		reqNode.Header.Set("Authorization", "Bearer "+token)
		respNode := mustDo(t, client, reqNode)
		defer func() { _ = respNode.Body.Close() }()
		assertStatus(t, respNode, http.StatusConflict)
		assertErrorCode(t, respNode, "WORKFLOW_ARCHIVED")

		// Archived workflows cannot be reverted directly (must delete/recreate).
		req2 := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/revert-to-draft", env.base, projID, workflowID), nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2 := mustDo(t, client, req2)
		defer func() { _ = resp2.Body.Close() }()
		assertStatus(t, resp2, http.StatusConflict)
		assertErrorCode(t, resp2, "WORKFLOW_NOT_ACTIVE")
	})
}

// ---------------------------------------------------------------------------
// Activation validation: done-status-undetermined and task-missing guards
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_ActivationValidation(t *testing.T) {
	env := newE2EEnv(t)
	username := "workflow-activation-user-" + uuid.NewString()
	seedTaskMemberUser(t, env, username, "workflowactivate1")
	client, token := taskMemberLogin(t, env, username, "workflowactivate1")
	projID := createProjectForTasksViaAPI(t, env, client, token)

	statuses := listTaskStatusesViaAPI(t, env, client, token, projID)
	backlogID := statusIDByName(statuses, "Backlog")
	doneID := statusIDByName(statuses, "Done")

	t.Run("activate_rejected_when_done_status_undetermined", func(t *testing.T) {
		task := createTaskViaAPI(t, env, client, token, projID, "Undetermined Done Task")
		workflowID := createWorkflowViaAPI(t, env, client, token, projID, "Undetermined Done Workflow")
		addWorkflowNodeViaAPI(t, env, client, token, projID, workflowID, task)

		// Break the auto-seeded chain's single terminal status by pointing
		// Done's "next" back at Backlog, closing the loop with zero terminal statuses.
		body := jsonBody(t, map[string]any{"status_id": doneID, "next_status_id": backlogID})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/status-transitions", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusCreated)

		req2 := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/activate", env.base, projID, workflowID), nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		resp2 := mustDo(t, client, req2)
		defer func() { _ = resp2.Body.Close() }()
		assertStatus(t, resp2, http.StatusBadRequest)
		assertErrorCode(t, resp2, "WORKFLOW_ACTIVATE_DONE_STATUS_UNDETERMINED")
	})

	t.Run("activate_rejected_when_node_task_deleted", func(t *testing.T) {
		task := createTaskViaAPI(t, env, client, token, projID, "Task To Delete")
		workflowID := createWorkflowViaAPI(t, env, client, token, projID, "Task Missing Workflow")
		addWorkflowNodeViaAPI(t, env, client, token, projID, workflowID, task)

		delReq := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/tasks/%s", env.base, projID, task), nil)
		delReq.Header.Set("Authorization", "Bearer "+token)
		delResp := mustDo(t, client, delReq)
		defer func() { _ = delResp.Body.Close() }()
		assertStatus(t, delResp, http.StatusOK)

		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/activate", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_ACTIVATE_TASK_MISSING")
	})
}

// ---------------------------------------------------------------------------
// "Which workflows is this task in" read path
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_ListWorkflowsForTask(t *testing.T) {
	env := newE2EEnv(t)
	username := "workflow-fortask-user-" + uuid.NewString()
	seedTaskMemberUser(t, env, username, "workflowfortask1")
	client, token := taskMemberLogin(t, env, username, "workflowfortask1")
	projID := createProjectForTasksViaAPI(t, env, client, token)

	sharedTask := createTaskViaAPI(t, env, client, token, projID, "Shared Task")
	loneTask := createTaskViaAPI(t, env, client, token, projID, "Lone Task")

	workflow1ID := createWorkflowViaAPI(t, env, client, token, projID, "Workflow One")
	workflow2ID := createWorkflowViaAPI(t, env, client, token, projID, "Workflow Two")

	addWorkflowNodeViaAPI(t, env, client, token, projID, workflow1ID, sharedTask)
	addWorkflowNodeViaAPI(t, env, client, token, projID, workflow2ID, sharedTask)

	t.Run("task_in_two_workflows", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/tasks/%s/workflows", env.base, projID, sharedTask), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		items, _ := data["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("expected 2 workflows for the shared task, got %d", len(items))
		}
		got := map[string]bool{}
		for _, item := range items {
			m, _ := item.(map[string]any)
			id, _ := m["id"].(string)
			got[id] = true
		}
		if !got[workflow1ID] || !got[workflow2ID] {
			t.Errorf("expected both workflow ids present, got %v", got)
		}
	})

	t.Run("task_in_no_workflows", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/tasks/%s/workflows", env.base, projID, loneTask), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)
		var env2 envelope
		decodeJSON(t, resp, &env2)
		data := assertDataMap(t, env2)
		items, _ := data["items"].([]any)
		if len(items) != 0 {
			t.Errorf("expected 0 workflows for a task that belongs to none, got %d", len(items))
		}
	})
}

// ---------------------------------------------------------------------------
// General validation / not-found / cross-workflow error cases
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_ErrorCases(t *testing.T) {
	env := newE2EEnv(t)
	username := "workflow-errors-user-" + uuid.NewString()
	seedTaskMemberUser(t, env, username, "workflowerrors1")
	client, token := taskMemberLogin(t, env, username, "workflowerrors1")
	projID := createProjectForTasksViaAPI(t, env, client, token)

	t.Run("create_workflow_missing_name_rejected", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"name": "", "description": ""})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows", env.base, projID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
		assertErrorCode(t, resp, "WORKFLOW_NAME_INVALID")
	})

	t.Run("get_nonexistent_workflow_not_found", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, uuid.NewString()), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNotFound)
		assertErrorCode(t, resp, "WORKFLOW_NOT_FOUND")
	})

	t.Run("get_workflow_invalid_id_bad_request", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/not-a-uuid", env.base, projID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("workflow_not_visible_from_a_different_project", func(t *testing.T) {
		workflowID := createWorkflowViaAPI(t, env, client, token, projID, "Scoped Workflow")
		otherProjID := createProjectForTasksViaAPI(t, env, client, token)

		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, otherProjID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNotFound)
		assertErrorCode(t, resp, "WORKFLOW_NOT_FOUND")
	})

	t.Run("node_from_a_different_workflow_not_found", func(t *testing.T) {
		task := createTaskViaAPI(t, env, client, token, projID, "Cross Workflow Task")
		workflowAID := createWorkflowViaAPI(t, env, client, token, projID, "Workflow A")
		workflowBID := createWorkflowViaAPI(t, env, client, token, projID, "Workflow B")
		nodeData := addWorkflowNodeViaAPI(t, env, client, token, projID, workflowAID, task)
		nodeID, _ := nodeData["id"].(string)

		req := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/nodes/%s", env.base, projID, workflowBID, nodeID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNotFound)
		assertErrorCode(t, resp, "WORKFLOW_NODE_NOT_FOUND")
	})

	t.Run("delete_nonexistent_workflow_not_found", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, uuid.NewString()), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("list_workflows_invalid_status_filter_rejected", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows?status=bogus", env.base, projID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDo(t, client, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusBadRequest)
	})
}

// ---------------------------------------------------------------------------
// Authorization: project-scoped workflows.read/workflows.write permissions
// ---------------------------------------------------------------------------

func TestE2EWorkflowManagement_Authorization(t *testing.T) {
	env := newE2EEnv(t)

	ownerUsername := "workflow-authz-owner-" + uuid.NewString()
	seedTaskMemberUser(t, env, ownerUsername, "workflowauthzowner1")
	ownerClient, ownerToken := taskMemberLogin(t, env, ownerUsername, "workflowauthzowner1")
	projID := createProjectForTasksViaAPI(t, env, ownerClient, ownerToken)

	workflowID := createWorkflowViaAPI(t, env, ownerClient, ownerToken, projID, "Authz Workflow")

	readonlyRoleID := createProjectRoleWithPermsViaAPI(t, env, ownerClient, ownerToken, projID, "workflow-viewer-"+uuid.NewString(),
		map[string]any{"projects.read": true, "workflows.read": true})

	readonlyUsername := "workflow-authz-viewer-" + uuid.NewString()
	seedUser(t, env, readonlyUsername, "workflowauthzviewer1", "Workflow Viewer")
	readonlyUser, err := env.userRepo.FindByUsername(env.ctx, readonlyUsername)
	if err != nil {
		t.Fatalf("find readonly user: %v", err)
	}
	addMemberViaAPI(t, env, ownerClient, ownerToken, projID, readonlyUser.ID.String(), readonlyRoleID)
	readonlyClient, readonlyToken := loginUser(t, env, readonlyUsername, "workflowauthzviewer1")

	t.Run("readonly_can_list_and_get_workflows", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows", env.base, projID), nil)
		req.Header.Set("Authorization", "Bearer "+readonlyToken)
		resp := mustDo(t, readonlyClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusOK)

		req2 := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, workflowID), nil)
		req2.Header.Set("Authorization", "Bearer "+readonlyToken)
		resp2 := mustDo(t, readonlyClient, req2)
		defer func() { _ = resp2.Body.Close() }()
		assertStatus(t, resp2, http.StatusOK)
	})

	t.Run("readonly_cannot_create_workflow", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"name": "Should Fail", "description": ""})
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows", env.base, projID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+readonlyToken)
		resp := mustDo(t, readonlyClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusForbidden)
		assertErrorCode(t, resp, "FORBIDDEN")
	})

	t.Run("readonly_cannot_update_workflow", func(t *testing.T) {
		body := jsonBody(t, map[string]any{"name": "Hacked"})
		req := mustRequest(env.ctx, t, http.MethodPatch,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, workflowID), body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+readonlyToken)
		resp := mustDo(t, readonlyClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("readonly_cannot_delete_workflow", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodDelete,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+readonlyToken)
		resp := mustDo(t, readonlyClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("readonly_cannot_activate_workflow", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodPost,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows/%s/activate", env.base, projID, workflowID), nil)
		req.Header.Set("Authorization", "Bearer "+readonlyToken)
		resp := mustDo(t, readonlyClient, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusForbidden)
	})

	t.Run("unauthenticated_request_rejected", func(t *testing.T) {
		req := mustRequest(env.ctx, t, http.MethodGet,
			fmt.Sprintf("%s/api/v1/projects/%s/workflows", env.base, projID), nil)
		resp := mustDo(t, &http.Client{}, req)
		defer func() { _ = resp.Body.Close() }()
		assertStatus(t, resp, http.StatusUnauthorized)
	})
}
