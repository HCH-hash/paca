import { describe, expect, it, vi } from "vitest";

import {
	RECOMMENDED_NODE_GAP_X,
	RECOMMENDED_NODE_GAP_Y,
} from "../../tools/workflow-orchestration.js";
import {
	getWorkflowTools,
	handleWorkflowTool,
} from "../../tools/workflow-tools.js";

const workflow = {
	id: "wf1",
	project_id: "p1",
	name: "Release pipeline",
	description: "",
	status: "draft",
	created_at: "2024-01-01T00:00:00Z",
	updated_at: "2024-01-01T00:00:00Z",
};

function makeNode(id: string, taskId: string, posX = 0, posY = 0) {
	return {
		id,
		workflow_id: "wf1",
		task_id: taskId,
		pos_x: posX,
		pos_y: posY,
		created_at: "2024-01-01T00:00:00Z",
		updated_at: "2024-01-01T00:00:00Z",
	};
}

function makeEdge(id: string, sourceNodeId: string, targetNodeId: string) {
	return {
		id,
		workflow_id: "wf1",
		source_node_id: sourceNodeId,
		target_node_id: targetNodeId,
		created_at: "2024-01-01T00:00:00Z",
	};
}

function makeTransition(
	id: string,
	statusId: string,
	nextStatusId: string | null,
) {
	return {
		id,
		workflow_id: "wf1",
		status_id: statusId,
		next_status_id: nextStatusId,
		created_at: "2024-01-01T00:00:00Z",
		updated_at: "2024-01-01T00:00:00Z",
	};
}

function makeGraph(
	overrides: Partial<{
		workflow: any;
		nodes: any[];
		edges: any[];
		status_transitions: any[];
	}> = {},
) {
	return {
		workflow: overrides.workflow ?? workflow,
		nodes: overrides.nodes ?? [],
		edges: overrides.edges ?? [],
		status_transitions: overrides.status_transitions ?? [],
	};
}

function makeWorkflowClient(overrides: Record<string, any> = {}) {
	return {
		listWorkflows: vi.fn().mockResolvedValue([workflow]),
		getWorkflow: vi.fn().mockResolvedValue(makeGraph()),
		createWorkflow: vi.fn().mockResolvedValue(workflow),
		updateWorkflow: vi.fn().mockResolvedValue({ ...workflow, name: "Renamed" }),
		deleteWorkflow: vi.fn().mockResolvedValue(undefined),
		activateWorkflow: vi
			.fn()
			.mockResolvedValue({ ...workflow, status: "active" }),
		archiveWorkflow: vi
			.fn()
			.mockResolvedValue({ ...workflow, status: "archived" }),
		revertWorkflowToDraft: vi
			.fn()
			.mockResolvedValue({ ...workflow, status: "draft" }),
		addWorkflowNode: vi
			.fn()
			.mockImplementation((_p: string, _w: string, input: any) =>
				Promise.resolve(
					makeNode(
						`n-${input.task_id}`,
						input.task_id,
						input.pos_x,
						input.pos_y,
					),
				),
			),
		updateWorkflowNode: vi
			.fn()
			.mockImplementation(
				(_p: string, _w: string, nodeId: string, input: any) =>
					Promise.resolve({ ...makeNode(nodeId, "unknown"), ...input }),
			),
		removeWorkflowNode: vi.fn().mockResolvedValue(undefined),
		setWorkflowStatusTransition: vi
			.fn()
			.mockImplementation((_p: string, _w: string, input: any) =>
				Promise.resolve(
					makeTransition(
						`tr-${input.status_id}`,
						input.status_id,
						input.next_status_id ?? null,
					),
				),
			),
		removeWorkflowStatusTransition: vi.fn().mockResolvedValue(undefined),
		addWorkflowEdge: vi
			.fn()
			.mockImplementation((_p: string, _w: string, input: any) =>
				Promise.resolve(
					makeEdge(
						`e-${input.source_node_id}-${input.target_node_id}`,
						input.source_node_id,
						input.target_node_id,
					),
				),
			),
		removeWorkflowEdge: vi.fn().mockResolvedValue(undefined),
		...overrides,
	} as any;
}

// ---------------------------------------------------------------------------
// getWorkflowTools
// ---------------------------------------------------------------------------

describe("getWorkflowTools", () => {
	it("returns exactly 4 tools", () => {
		expect(getWorkflowTools()).toHaveLength(4);
	});

	it("returns exactly the 4 final tool names", () => {
		const names = getWorkflowTools().map((t) => t.name);
		expect(names).toEqual([
			"get_workflow",
			"create_workflow",
			"update_workflow",
			"delete_workflow",
		]);
	});
});

// ---------------------------------------------------------------------------
// get_workflow
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - get_workflow", () => {
	it("with workflowId fetches and formats the full graph", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "t1")],
					edges: [makeEdge("e1", "n1", "n2")],
				}),
			),
		});
		const result = await handleWorkflowTool(
			"get_workflow",
			{ projectId: "p1", workflowId: "wf1" },
			client,
		);
		expect(client.getWorkflow).toHaveBeenCalledWith("p1", "wf1");
		expect(result.content[0].text).toContain("node n1");
		expect(result.content[0].text).toContain("edge e1");
	});

	it("includes each node's position, so an agent extending an existing workflow can see and continue the layout", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "t1", 900, 400)],
				}),
			),
		});
		const result = await handleWorkflowTool(
			"get_workflow",
			{ projectId: "p1", workflowId: "wf1" },
			client,
		);
		expect(result.content[0].text).toContain("position=(900, 400)");
	});

	it("without workflowId lists workflows in the project", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"get_workflow",
			{ projectId: "p1" },
			client,
		);
		expect(client.listWorkflows).toHaveBeenCalledWith("p1", undefined);
		expect(client.getWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("Release pipeline");
	});

	it("without workflowId passes the status filter through", async () => {
		const client = makeWorkflowClient();
		await handleWorkflowTool(
			"get_workflow",
			{ projectId: "p1", status: "active" },
			client,
		);
		expect(client.listWorkflows).toHaveBeenCalledWith("p1", "active");
	});

	it("without workflowId reports an empty project", async () => {
		const client = makeWorkflowClient({
			listWorkflows: vi.fn().mockResolvedValue([]),
		});
		const result = await handleWorkflowTool(
			"get_workflow",
			{ projectId: "p1" },
			client,
		);
		expect(result.content[0].text).toContain("No automation workflows");
	});
});

// ---------------------------------------------------------------------------
// create_workflow
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - create_workflow", () => {
	it("bare call only creates the workflow shell", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"create_workflow",
			{ projectId: "p1", name: "Release pipeline" },
			client,
		);
		expect(client.createWorkflow).toHaveBeenCalledWith("p1", {
			name: "Release pipeline",
			description: undefined,
		});
		expect(client.addWorkflowNode).not.toHaveBeenCalled();
		expect(client.setWorkflowStatusTransition).not.toHaveBeenCalled();
		expect(client.addWorkflowEdge).not.toHaveBeenCalled();
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("Created draft workflow");
	});

	it("builds nodes, status transitions, and edges in one call", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"create_workflow",
			{
				projectId: "p1",
				name: "Release pipeline",
				nodes: [
					{ taskId: "t1", posX: 1, posY: 2 },
					{ taskId: "t2", posX: 400, posY: 0 },
				],
				statusTransitions: [{ statusId: "s-ready", nextStatusId: "s-done" }],
				edges: [{ sourceTaskId: "t1", targetTaskId: "t2" }],
			},
			client,
		);

		expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
			task_id: "t1",
			pos_x: 1,
			pos_y: 2,
		});
		expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
			task_id: "t2",
			pos_x: 400,
			pos_y: 0,
		});
		expect(client.setWorkflowStatusTransition).toHaveBeenCalledWith(
			"p1",
			"wf1",
			{ status_id: "s-ready", next_status_id: "s-done" },
		);
		// The edge references taskIds, but the REST call must resolve them to
		// the internal node ids returned by addWorkflowNode above.
		expect(client.addWorkflowEdge).toHaveBeenCalledWith("p1", "wf1", {
			source_node_id: "n-t1",
			target_node_id: "n-t2",
		});
		expect(result.content[0].text).toContain("Created draft workflow");
	});

	it("warns (without blocking) when nodes are set closer than the recommended minimum spacing", async () => {
		// Models the reported real-world case: an agent choosing small,
		// evenly-spaced values regardless of the stated minimum. Gap is
		// derived from the constant (rather than hardcoded) so this test
		// keeps working regardless of future threshold adjustments.
		const gap = Math.floor(RECOMMENDED_NODE_GAP_X / 2);
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"create_workflow",
			{
				projectId: "p1",
				name: "Release pipeline",
				nodes: [
					{ taskId: "t1", posX: gap, posY: 0 },
					{ taskId: "t2", posX: gap * 2, posY: 0 },
				],
			},
			client,
		);
		// The positions are still applied exactly as requested — the check is
		// advisory feedback, not an enforced correction.
		expect(client.addWorkflowNode).toHaveBeenCalledTimes(2);
		expect(result.content[0].text).toContain("Warning:");
		expect(result.content[0].text).toContain(
			"close enough to visually crowd or overlap",
		);
		expect(result.content[0].text).toContain(
			`t1 (${gap}, 0) and t2 (${gap * 2}, 0)`,
		);
	});

	it("does not warn when nodes are spaced at least the recommended minimum apart", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"create_workflow",
			{
				projectId: "p1",
				name: "Release pipeline",
				nodes: [
					{ taskId: "t1", posX: 0, posY: 0 },
					{ taskId: "t2", posX: RECOMMENDED_NODE_GAP_X, posY: 0 },
					{ taskId: "t3", posX: 0, posY: RECOMMENDED_NODE_GAP_Y },
				],
			},
			client,
		);
		expect(result.content[0].text).not.toContain("Warning:");
	});

	it("does not include a failed node's position in the spacing check", async () => {
		const client = makeWorkflowClient({
			addWorkflowNode: vi.fn().mockImplementation((_p, _w, input: any) => {
				if (input.task_id === "bad") {
					return Promise.reject(new Error("boom"));
				}
				return Promise.resolve({
					id: `n-${input.task_id}`,
					workflow_id: "wf1",
					task_id: input.task_id,
					pos_x: input.pos_x,
					pos_y: input.pos_y,
					created_at: "2024-01-01T00:00:00Z",
					updated_at: "2024-01-01T00:00:00Z",
				});
			}),
		});
		const result = await handleWorkflowTool(
			"create_workflow",
			{
				projectId: "p1",
				name: "Release pipeline",
				// "bad" fails to create, but is positioned right on top of "good" —
				// that shouldn't produce a spacing warning about a node that was
				// never actually added to the canvas.
				nodes: [
					{ taskId: "good", posX: 0, posY: 0 },
					{ taskId: "bad", posX: 10, posY: 10 },
				],
			},
			client,
		);
		expect(result.content[0].text).toContain("failed");
		expect(result.content[0].text).not.toContain("Warning:");
	});

	it("reports a failed edge referencing an unknown taskId without blocking siblings", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"create_workflow",
			{
				projectId: "p1",
				name: "Release pipeline",
				nodes: [
					{ taskId: "t1", posX: 0, posY: 0 },
					{ taskId: "t2", posX: 400, posY: 0 },
				],
				edges: [
					{ sourceTaskId: "not-a-node", targetTaskId: "t1" },
					{ sourceTaskId: "t1", targetTaskId: "t2" },
				],
			},
			client,
		);
		// The valid edge still goes through...
		expect(client.addWorkflowEdge).toHaveBeenCalledTimes(1);
		expect(client.addWorkflowEdge).toHaveBeenCalledWith("p1", "wf1", {
			source_node_id: "n-t1",
			target_node_id: "n-t2",
		});
		// ...and the bad one is reported, not thrown.
		expect(result.content[0].text).toContain("failed");
		expect(result.content[0].text).toContain("not-a-node");
	});

	it("activates immediately when activate is true and everything succeeded", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"create_workflow",
			{
				projectId: "p1",
				name: "Release pipeline",
				nodes: [{ taskId: "t1", posX: 0, posY: 0 }],
				activate: true,
			},
			client,
		);
		expect(client.activateWorkflow).toHaveBeenCalledWith("p1", "wf1");
		expect(result.content[0].text).toContain("now active");
	});

	it("skips activation when an earlier item failed", async () => {
		const client = makeWorkflowClient({
			addWorkflowNode: vi.fn().mockRejectedValue(new Error("boom")),
		});
		const result = await handleWorkflowTool(
			"create_workflow",
			{
				projectId: "p1",
				name: "Release pipeline",
				nodes: [{ taskId: "t1", posX: 0, posY: 0 }],
				activate: true,
			},
			client,
		);
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("Skipped activation");
	});
});

// ---------------------------------------------------------------------------
// update_workflow - rename
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - update_workflow rename", () => {
	it("renames/describes a workflow", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"update_workflow",
			{ projectId: "p1", workflowId: "wf1", name: "Renamed" },
			client,
		);
		expect(client.updateWorkflow).toHaveBeenCalledWith("p1", "wf1", {
			name: "Renamed",
			description: undefined,
		});
		expect(result.content[0].text).toContain("Renamed");
	});

	it("does nothing and says so when no fields are given", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"update_workflow",
			{ projectId: "p1", workflowId: "wf1" },
			client,
		);
		expect(client.updateWorkflow).not.toHaveBeenCalled();
		expect(client.getWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("No changes requested");
	});
});

// ---------------------------------------------------------------------------
// update_workflow - nodes
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - update_workflow nodes", () => {
	it("adds a node for a taskId with no existing node", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph({ nodes: [] })),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 10, posY: 20 }] },
			},
			client,
		);
		expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
			task_id: "t1",
			pos_x: 10,
			pos_y: 20,
		});
		expect(client.updateWorkflowNode).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("1 created");
	});

	it("repositions an existing node instead of re-adding it", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi
				.fn()
				.mockResolvedValue(makeGraph({ nodes: [makeNode("n1", "t1")] })),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 10, posY: 20 }] },
			},
			client,
		);
		expect(client.updateWorkflowNode).toHaveBeenCalledWith("p1", "wf1", "n1", {
			pos_x: 10,
			pos_y: 20,
		});
		expect(client.addWorkflowNode).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("1 updated");
	});

	it("warns (without blocking) when repositioned nodes end up closer than the recommended minimum spacing", async () => {
		// Mirrors an actual reported payload: several nodes in the same row
		// evenly spaced well under the recommended minimum. Step is derived
		// from the gap constant (rather than hardcoded) so this test keeps
		// working regardless of future threshold adjustments — the widest
		// pair (2 steps) must still land under RECOMMENDED_NODE_GAP_X.
		const step = Math.floor(RECOMMENDED_NODE_GAP_X / 4);
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [
						makeNode("n1", "t1"),
						makeNode("n2", "t2"),
						makeNode("n3", "t3"),
					],
				}),
			),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: {
					set: [
						{ taskId: "t1", posX: step, posY: 200 },
						{ taskId: "t2", posX: step * 2, posY: 200 },
						{ taskId: "t3", posX: step * 3, posY: 200 },
					],
				},
			},
			client,
		);
		expect(client.updateWorkflowNode).toHaveBeenCalledTimes(3);
		expect(result.content[0].text).toContain("Warning:");
		expect(result.content[0].text).toContain("3 pair(s)");
	});

	it("does not warn when repositioned nodes are spaced at least the recommended minimum apart", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "t1"), makeNode("n2", "t2")],
				}),
			),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: {
					set: [
						{ taskId: "t1", posX: 0, posY: 0 },
						{ taskId: "t2", posX: RECOMMENDED_NODE_GAP_X, posY: 0 },
					],
				},
			},
			client,
		);
		expect(result.content[0].text).not.toContain("Warning:");
	});

	// Regression test for a reported bug: a workflow built across several
	// update_workflow calls had two nodes end up 250px apart (under the
	// 300px minimum) with no warning, because the two nodes were never in
	// the same call's `nodes.set` array together — t1 already existed in
	// the graph (added by an earlier call); this call only sets t2, close
	// to t1's *existing* position, so the warning must compare against the
	// pre-edit graph, not just the nodes this call happens to touch.
	it("warns when a newly added node is too close to a pre-existing node from an earlier call", async () => {
		const step = Math.floor(RECOMMENDED_NODE_GAP_X / 2);
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "t1", 0, 0)],
				}),
			),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t2", posX: step, posY: 0 }] },
			},
			client,
		);
		expect(client.addWorkflowNode).toHaveBeenCalledTimes(1);
		expect(result.content[0].text).toContain("Warning:");
		expect(result.content[0].text).toContain("t1");
		expect(result.content[0].text).toContain("t2");
	});

	it("also surfaces pre-existing crowding between two untouched nodes, since the check now covers the whole graph", async () => {
		// e1/e2 are already crowded before this call and neither is in
		// nodes.set — checking the full graph (the simpler, chosen design)
		// means this still gets flagged rather than staying invisible forever.
		const step = Math.floor(RECOMMENDED_NODE_GAP_X / 2);
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "e1", 0, 0), makeNode("n2", "e2", step, 0)],
				}),
			),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: {
					set: [{ taskId: "t3", posX: 10_000, posY: 10_000 }],
				},
			},
			client,
		);
		expect(result.content[0].text).toContain("Warning:");
		expect(result.content[0].text).toContain("e1");
		expect(result.content[0].text).toContain("e2");
	});

	// Regression test for an edge case in the fix above: a nodes.set entry
	// that FAILS for a pre-existing node must not vanish from the spacing
	// check — it still occupies its old (unchanged) position on the canvas,
	// so that position must still be compared against everything else.
	it("still checks a pre-existing node's old position when its own reposition attempt fails", async () => {
		const step = Math.floor(RECOMMENDED_NODE_GAP_X / 2);
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "t1", 0, 0)],
				}),
			),
			updateWorkflowNode: vi.fn().mockRejectedValue(new Error("boom")),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: {
					set: [
						{ taskId: "t1", posX: 5, posY: 0 },
						{ taskId: "t2", posX: step, posY: 0 },
					],
				},
			},
			client,
		);
		expect(result.content[0].text).toContain("failed");
		expect(result.content[0].text).toContain("Warning:");
		expect(result.content[0].text).toContain("t1");
		expect(result.content[0].text).toContain("t2");
	});

	it("removes a node by taskId", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi
				.fn()
				.mockResolvedValue(makeGraph({ nodes: [makeNode("n1", "t1")] })),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{ projectId: "p1", workflowId: "wf1", nodes: { remove: ["t1"] } },
			client,
		);
		expect(client.removeWorkflowNode).toHaveBeenCalledWith("p1", "wf1", "n1");
		expect(result.content[0].text).toContain("1 removed");
	});

	it("treats removing a nonexistent taskId as a no-op, not a failure", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph({ nodes: [] })),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{ projectId: "p1", workflowId: "wf1", nodes: { remove: ["ghost"] } },
			client,
		);
		expect(client.removeWorkflowNode).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("1 skipped");
		expect(result.content[0].text).not.toContain("failed");
	});

	it("adds and removes different nodes in the same call", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi
				.fn()
				.mockResolvedValue(makeGraph({ nodes: [makeNode("n1", "t1")] })),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t2", posX: 0, posY: 0 }], remove: ["t1"] },
			},
			client,
		);
		expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
			task_id: "t2",
			pos_x: 0,
			pos_y: 0,
		});
		expect(client.removeWorkflowNode).toHaveBeenCalledWith("p1", "wf1", "n1");
	});

	it("dedupes repeated entries for the same taskId, keeping the last", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph({ nodes: [] })),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: {
					set: [
						{ taskId: "t3", posX: 0, posY: 0 },
						{ taskId: "t3", posX: 100, posY: 200 },
					],
				},
			},
			client,
		);
		expect(client.addWorkflowNode).toHaveBeenCalledTimes(1);
		expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
			task_id: "t3",
			pos_x: 100,
			pos_y: 200,
		});
	});
});

// ---------------------------------------------------------------------------
// update_workflow - status rules / transitions
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - update_workflow status transitions", () => {
	it("sets a status transition", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph()),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				statusTransitions: {
					set: [{ statusId: "s-ready", nextStatusId: "s-done" }],
				},
			},
			client,
		);
		expect(client.setWorkflowStatusTransition).toHaveBeenCalledWith(
			"p1",
			"wf1",
			{ status_id: "s-ready", next_status_id: "s-done" },
		);
	});

	it("removes a status transition by statusId", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					status_transitions: [makeTransition("tr1", "s-ready", "s-done")],
				}),
			),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				statusTransitions: { remove: ["s-ready"] },
			},
			client,
		);
		expect(client.removeWorkflowStatusTransition).toHaveBeenCalledWith(
			"p1",
			"wf1",
			"tr1",
		);
	});
});

// ---------------------------------------------------------------------------
// update_workflow - edges
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - update_workflow edges", () => {
	it("adds an edge between two existing nodes", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi
				.fn()
				.mockResolvedValue(
					makeGraph({ nodes: [makeNode("n1", "t1"), makeNode("n2", "t2")] }),
				),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				edges: { add: [{ sourceTaskId: "t1", targetTaskId: "t2" }] },
			},
			client,
		);
		expect(client.addWorkflowEdge).toHaveBeenCalledWith("p1", "wf1", {
			source_node_id: "n1",
			target_node_id: "n2",
		});
	});

	it("removes an edge by its task-id pair", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "t1"), makeNode("n2", "t2")],
					edges: [makeEdge("e1", "n1", "n2")],
				}),
			),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				edges: { remove: [{ sourceTaskId: "t1", targetTaskId: "t2" }] },
			},
			client,
		);
		expect(client.removeWorkflowEdge).toHaveBeenCalledWith("p1", "wf1", "e1");
	});

	it("treats removing an edge whose node was removed in the same call as a no-op", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					nodes: [makeNode("n1", "t1"), makeNode("n2", "t2")],
					edges: [makeEdge("e1", "n1", "n2")],
				}),
			),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { remove: ["t1"] },
				edges: { remove: [{ sourceTaskId: "t1", targetTaskId: "t2" }] },
			},
			client,
		);
		expect(client.removeWorkflowNode).toHaveBeenCalledWith("p1", "wf1", "n1");
		expect(client.removeWorkflowEdge).not.toHaveBeenCalled();
		expect(result.content[0].text).not.toContain("failed");
	});
});

// ---------------------------------------------------------------------------
// update_workflow - partial failure
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - update_workflow partial failure", () => {
	it("attempts every sibling item even when one fails", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph({ nodes: [] })),
			addWorkflowNode: vi
				.fn()
				.mockImplementation((_p: string, _w: string, input: any) => {
					if (input.task_id === "bad-task") {
						return Promise.reject(new Error("boom"));
					}
					return Promise.resolve(makeNode(`n-${input.task_id}`, input.task_id));
				}),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: {
					set: [
						{ taskId: "good-task", posX: 0, posY: 0 },
						{ taskId: "bad-task", posX: 400, posY: 0 },
					],
				},
			},
			client,
		);
		expect(client.addWorkflowNode).toHaveBeenCalledTimes(2);
		expect(result.content[0].text).toContain("1 created");
		expect(result.content[0].text).toContain("1 failed");
		expect(result.content[0].text).toContain("boom");
	});
});

// ---------------------------------------------------------------------------
// update_workflow - lifecycle
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - update_workflow lifecycle", () => {
	it("activates a workflow", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"update_workflow",
			{ projectId: "p1", workflowId: "wf1", status: "active" },
			client,
		);
		expect(client.activateWorkflow).toHaveBeenCalledWith("p1", "wf1");
		expect(result.content[0].text).toContain("now active");
	});

	it("archives a workflow", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"update_workflow",
			{ projectId: "p1", workflowId: "wf1", status: "archived" },
			client,
		);
		expect(client.archiveWorkflow).toHaveBeenCalledWith("p1", "wf1");
		expect(result.content[0].text).toContain("now archived");
	});

	it("activates AFTER graph edits requested in the same call", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph({ nodes: [] })),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
				status: "active",
			},
			client,
		);
		const nodeCallOrder = client.addWorkflowNode.mock.invocationCallOrder[0];
		const activateCallOrder =
			client.activateWorkflow.mock.invocationCallOrder[0];
		expect(nodeCallOrder).toBeLessThan(activateCallOrder);
	});

	it("reverts to draft BEFORE graph edits requested in the same call", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph({ nodes: [] })),
		});
		await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				status: "draft",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
			},
			client,
		);
		const revertCallOrder =
			client.revertWorkflowToDraft.mock.invocationCallOrder[0];
		const fetchCallOrder = client.getWorkflow.mock.invocationCallOrder[0];
		expect(revertCallOrder).toBeLessThan(fetchCallOrder);
	});

	it("repositions an existing node directly on an active workflow, no revert needed", async () => {
		// Graph edits — including a position-only change — work directly
		// against an active workflow now; only 'archived' locks the graph.
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(
				makeGraph({
					workflow: { ...workflow, status: "active" },
					nodes: [makeNode("n1", "t1", 999, 999)],
				}),
			),
		});

		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 50, posY: 50 }] },
			},
			client,
		);
		expect(client.revertWorkflowToDraft).not.toHaveBeenCalled();
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(client.updateWorkflowNode).toHaveBeenCalledWith("p1", "wf1", "n1", {
			pos_x: 50,
			pos_y: 50,
		});
		expect(result.content[0].text).toContain("1 updated");
	});

	it("skips graph edits entirely when the revert-to-draft fails", async () => {
		const client = makeWorkflowClient({
			revertWorkflowToDraft: vi
				.fn()
				.mockRejectedValue(new Error("workflow is archived")),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				status: "draft",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
			},
			client,
		);
		expect(client.getWorkflow).not.toHaveBeenCalled();
		expect(client.addWorkflowNode).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("Could not revert to draft");
		expect(result.content[0].text).toContain("Skipped all graph edits");
	});

	it("applies graph edits directly to an active workflow, no status param needed", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi
				.fn()
				.mockResolvedValue(
					makeGraph({ workflow: { ...workflow, status: "active" } }),
				),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
			},
			client,
		);
		expect(client.revertWorkflowToDraft).not.toHaveBeenCalled();
		expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
			task_id: "t1",
			pos_x: 0,
			pos_y: 0,
		});
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).not.toContain("reverted");
	});

	it("skips graph edits for an archived workflow", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi
				.fn()
				.mockResolvedValue(
					makeGraph({ workflow: { ...workflow, status: "archived" } }),
				),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
			},
			client,
		);
		expect(client.revertWorkflowToDraft).not.toHaveBeenCalled();
		expect(client.addWorkflowNode).not.toHaveBeenCalled();
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("archived");
	});

	it("reports a failed graph edit on an active workflow without touching lifecycle", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi
				.fn()
				.mockResolvedValue(
					makeGraph({ workflow: { ...workflow, status: "active" } }),
				),
			addWorkflowNode: vi.fn().mockRejectedValue(new Error("boom")),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
			},
			client,
		);
		expect(client.revertWorkflowToDraft).not.toHaveBeenCalled();
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("boom");
	});

	it("status: 'draft' pauses automation and applies edits, staying in draft afterward", async () => {
		// getWorkflow reflects the POST-revert state here (draft, the
		// makeWorkflowClient default), same as the real API would return once
		// the explicit revertWorkflowToDraft call below has already landed.
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				status: "draft",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
			},
			client,
		);
		expect(client.revertWorkflowToDraft).toHaveBeenCalledTimes(1);
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).not.toContain("Temporarily reverted");
	});

	it("skips activation when an earlier step in the same call failed", async () => {
		const client = makeWorkflowClient({
			getWorkflow: vi.fn().mockResolvedValue(makeGraph({ nodes: [] })),
			addWorkflowNode: vi.fn().mockRejectedValue(new Error("boom")),
		});
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				nodes: { set: [{ taskId: "t1", posX: 0, posY: 0 }] },
				status: "active",
			},
			client,
		);
		expect(client.activateWorkflow).not.toHaveBeenCalled();
		expect(result.content[0].text).toContain("Skipped activation");
	});
});

// ---------------------------------------------------------------------------
// delete_workflow
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - delete_workflow", () => {
	it("deletes a workflow", async () => {
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"delete_workflow",
			{ projectId: "p1", workflowId: "wf1" },
			client,
		);
		expect(client.deleteWorkflow).toHaveBeenCalledWith("p1", "wf1");
		expect(result.content[0].text).toContain("deleted successfully");
	});
});

// ---------------------------------------------------------------------------
// invalid arguments
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - invalid arguments", () => {
	it("clearly reports a wrong node field name (e.g. 'position' instead of posX/posY) instead of rejecting or dumping raw Zod JSON", async () => {
		// Models an agent that invents a plausible-but-wrong field — a single
		// rank number, matching the `position` field used by other Paca tools
		// like list_task_statuses/move_task — instead of this tool's
		// posX/posY. Before the fix, this failed Zod validation with 16+
		// near-duplicate "Required" issues (2 per node) and no indication
		// `position` itself was the problem, and the resulting ZodError
		// wasn't even caught by the outer handler (see the unknown-tool test
		// above), so it wouldn't have reached the agent as a readable result
		// at all.
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool(
			"update_workflow",
			{
				projectId: "p1",
				workflowId: "wf1",
				status: "draft",
				nodes: {
					set: [
						{ taskId: "t1", position: 1 },
						{ taskId: "t2", position: 2 },
						{ taskId: "t3", position: 3 },
					],
				},
			},
			client,
		);
		expect(result.isError).toBe(true);
		const text = result.content[0].text;
		expect(text).toContain("posX");
		expect(text).toContain("posY");
		expect(text).toContain("Unrecognized key");
		expect(text).toContain("position");
		// Deduped across the 3 nodes, not one near-identical line per node.
		expect(text).toContain("(x3)");
		// Validation fails before any of the call's logic runs.
		expect(client.revertWorkflowToDraft).not.toHaveBeenCalled();
		expect(client.addWorkflowNode).not.toHaveBeenCalled();
	});
});

// ---------------------------------------------------------------------------
// unknown tool
// ---------------------------------------------------------------------------

describe("handleWorkflowTool - unknown tool", () => {
	it("returns an isError result for an unknown tool name instead of rejecting", async () => {
		// handleWorkflowTool catches internally rather than relying on
		// handleToolCall's outer try/catch, which only sees synchronous
		// throws — a bare `return handleWorkflowTool(...)` there does not
		// catch this function's own async rejections (see the comment above
		// handleWorkflowTool). Every error path — including this one — must
		// resolve to a normal tool result, never reject.
		const client = makeWorkflowClient();
		const result = await handleWorkflowTool("not_a_real_tool", {}, client);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("Unknown workflow tool");
	});
});
