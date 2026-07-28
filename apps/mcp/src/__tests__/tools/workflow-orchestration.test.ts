import { describe, expect, it, vi } from "vitest";

import {
	anyHasFailure,
	applyEdges,
	applyNodes,
	applyStatusTransitions,
	buildGraphIndexes,
	type CategoryResult,
	checkNodeSpacing,
	dedupeByKey,
	emptyGraphIndexes,
	extractApiErrorMessage,
	formatCategoryResult,
	type OrchestrationContext,
	RECOMMENDED_NODE_GAP_X,
	RECOMMENDED_NODE_GAP_Y,
} from "../../tools/workflow-orchestration.js";

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

/** A client stub exposing only the methods workflow-orchestration.ts calls. */
function makeClient(overrides: Record<string, any> = {}) {
	return {
		addWorkflowNode: vi.fn(),
		updateWorkflowNode: vi.fn(),
		removeWorkflowNode: vi.fn(),
		setWorkflowStatusTransition: vi.fn(),
		removeWorkflowStatusTransition: vi.fn(),
		addWorkflowEdge: vi.fn(),
		removeWorkflowEdge: vi.fn(),
		...overrides,
	} as any;
}

function makeCtx(client: any): OrchestrationContext {
	return { client, projectId: "p1", workflowId: "wf1" };
}

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

function emptyResult(): CategoryResult {
	return { items: [], hasFailure: false };
}

// ---------------------------------------------------------------------------
// anyHasFailure
// ---------------------------------------------------------------------------

describe("anyHasFailure", () => {
	it("returns false when called with no arguments", () => {
		expect(anyHasFailure()).toBe(false);
	});

	it("returns false when every argument is undefined", () => {
		expect(anyHasFailure(undefined, undefined)).toBe(false);
	});

	it("returns false when no result has a failure", () => {
		expect(anyHasFailure(emptyResult(), emptyResult())).toBe(false);
	});

	it("returns true when a single result has a failure", () => {
		expect(anyHasFailure({ items: [], hasFailure: true })).toBe(true);
	});

	it("returns true when any one of several results has a failure", () => {
		expect(
			anyHasFailure(
				emptyResult(),
				undefined,
				{ items: [], hasFailure: true },
				emptyResult(),
			),
		).toBe(true);
	});
});

// ---------------------------------------------------------------------------
// formatCategoryResult
// ---------------------------------------------------------------------------

describe("formatCategoryResult", () => {
	it("returns null when the result is undefined", () => {
		expect(formatCategoryResult("Nodes", undefined)).toBeNull();
	});

	it("returns null when the result has no items", () => {
		expect(formatCategoryResult("Nodes", emptyResult())).toBeNull();
	});

	it("formats a single created item with no detail line", () => {
		const result: CategoryResult = {
			items: [{ key: "t1", outcome: "created" }],
			hasFailure: false,
		};
		expect(formatCategoryResult("Nodes", result)).toBe("Nodes: 1 created");
	});

	it("only includes counts for outcomes that occurred, in created/updated/removed/skipped/failed order", () => {
		const result: CategoryResult = {
			items: [
				{ key: "t1", outcome: "removed" },
				{ key: "t2", outcome: "created" },
			],
			hasFailure: false,
		};
		expect(formatCategoryResult("Nodes", result)).toBe(
			"Nodes: 1 created, 1 removed",
		);
	});

	it("counts repeated outcomes and lists detail lines only for skipped/failed, in item order", () => {
		const result: CategoryResult = {
			items: [
				{ key: "t1", outcome: "created" },
				{ key: "t2", outcome: "updated" },
				{ key: "t3", outcome: "removed" },
				{ key: "t4", outcome: "failed", detail: "boom" },
				{ key: "t5", outcome: "skipped", detail: "already gone" },
				{ key: "t6", outcome: "created" },
			],
			hasFailure: true,
		};
		expect(formatCategoryResult("Nodes", result)).toBe(
			[
				"Nodes: 2 created, 1 updated, 1 removed, 1 skipped, 1 failed",
				"  - failed (t4): boom",
				"  - skipped (t5): already gone",
			].join("\n"),
		);
	});

	it("preserves the original relative order of skipped/failed lines even when failed appears before skipped", () => {
		const result: CategoryResult = {
			items: [
				{ key: "a", outcome: "failed", detail: "err-a" },
				{ key: "b", outcome: "skipped", detail: "skip-b" },
				{ key: "c", outcome: "failed", detail: "err-c" },
			],
			hasFailure: true,
		};
		const text = formatCategoryResult("Edges", result);
		const lines = text?.split("\n") ?? [];
		expect(lines[1]).toBe("  - failed (a): err-a");
		expect(lines[2]).toBe("  - skipped (b): skip-b");
		expect(lines[3]).toBe("  - failed (c): err-c");
	});
});

// ---------------------------------------------------------------------------
// checkNodeSpacing
// ---------------------------------------------------------------------------
//
// Regression coverage for prose-only spacing guidance not being enough on its
// own — agents kept picking small, evenly-spaced values (e.g. 200px) well
// under the stated minimum, so this after-the-call check gives concrete,
// per-pair feedback instead of relying entirely on the tool description.

describe("checkNodeSpacing", () => {
	it("returns null when there are fewer than two nodes", () => {
		expect(checkNodeSpacing([])).toBeNull();
		expect(checkNodeSpacing([{ taskId: "t1", posX: 0, posY: 0 }])).toBeNull();
	});

	it("returns null when every pair clears the minimum on at least one axis", () => {
		const positions = [
			{ taskId: "t1", posX: 0, posY: 0 },
			{ taskId: "t2", posX: RECOMMENDED_NODE_GAP_X, posY: 0 },
			{ taskId: "t3", posX: 0, posY: RECOMMENDED_NODE_GAP_Y },
		];
		expect(checkNodeSpacing(positions)).toBeNull();
	});

	it("warns when two nodes are closer than the minimum on BOTH axes at once", () => {
		// Gap is derived from the constant (rather than hardcoded) so this test
		// keeps working regardless of future threshold adjustments.
		const gap = Math.floor(RECOMMENDED_NODE_GAP_X / 2);
		const positions = [
			{ taskId: "t1", posX: 0, posY: 0 },
			{ taskId: "t2", posX: gap, posY: 0 },
		];
		const warning = checkNodeSpacing(positions);
		expect(warning).not.toBeNull();
		expect(warning).toContain("1 pair(s)");
		expect(warning).toContain(`t1 (0, 0) and t2 (${gap}, 0)`);
		expect(warning).toContain(
			`${gap}px apart horizontally, 0px apart vertically`,
		);
		expect(warning).toContain(`${RECOMMENDED_NODE_GAP_X}px apart horizontally`);
		expect(warning).toContain(`${RECOMMENDED_NODE_GAP_Y}px apart vertically`);
	});

	it("does not warn when nodes clear the Y minimum even if X is identical (different rows)", () => {
		const positions = [
			{ taskId: "t1", posX: 400, posY: 0 },
			{ taskId: "t2", posX: 400, posY: RECOMMENDED_NODE_GAP_Y },
		];
		expect(checkNodeSpacing(positions)).toBeNull();
	});

	it("treats exactly-at-the-minimum gap as acceptable (strict less-than, not less-or-equal)", () => {
		const positions = [
			{ taskId: "t1", posX: 0, posY: 0 },
			{ taskId: "t2", posX: RECOMMENDED_NODE_GAP_X, posY: 0 },
		];
		expect(checkNodeSpacing(positions)).toBeNull();
	});

	it("reports only the 3 closest pairs plus a count of the rest, sorted closest-first", () => {
		// 5 nodes stacked in one tight row — 10 total pairs, all violating.
		// Step is derived from the gap constant (rather than hardcoded) so this
		// test keeps working regardless of future threshold adjustments — the
		// widest pair (4 steps) must still land under RECOMMENDED_NODE_GAP_X.
		const step = Math.floor(RECOMMENDED_NODE_GAP_X / 5);
		const positions = [0, 1, 2, 3, 4].map((i) => ({
			taskId: `t${i}`,
			posX: i * step,
			posY: 0,
		}));
		const warning = checkNodeSpacing(positions);
		expect(warning).toContain("10 pair(s)");
		expect(warning).toContain("...and 7 more pair(s) this close.");
		// The closest pair (one step apart, adjacent) should be listed first.
		const firstExampleLine = warning?.split("\n")[1];
		expect(firstExampleLine).toContain(`${step}px apart horizontally`);
	});

	it("never modifies the input positions — purely advisory", () => {
		const positions = [
			{ taskId: "t1", posX: 0, posY: 0 },
			{ taskId: "t2", posX: 50, posY: 0 },
		];
		const snapshot = JSON.parse(JSON.stringify(positions));
		checkNodeSpacing(positions);
		expect(positions).toEqual(snapshot);
	});

	// Regression coverage for the reported bug: a workflow built across
	// several update_workflow calls could have two nodes added in different
	// calls end up too close together (e.g. 250px, under the 300px minimum)
	// with no warning ever firing, because the two were never in the same
	// call's `positions` array together. The fix is at the call site (pass
	// the full graph, not just the nodes this call touches) rather than in
	// this function, so this just confirms a node newly added to a larger
	// list is still caught wherever it sits in that list.
	it("warns when a node anywhere in a larger positions list is too close to another", () => {
		const gap = Math.floor(RECOMMENDED_NODE_GAP_X / 2);
		const positions = [
			{ taskId: "pre-existing", posX: 0, posY: 0 },
			{ taskId: "unrelated", posX: 10_000, posY: 10_000 },
			{ taskId: "newly-added", posX: gap, posY: 0 },
		];
		const warning = checkNodeSpacing(positions);
		expect(warning).not.toBeNull();
		expect(warning).toContain("pre-existing");
		expect(warning).toContain("newly-added");
		expect(warning).not.toContain("unrelated");
	});
});

// ---------------------------------------------------------------------------
// buildGraphIndexes / emptyGraphIndexes
// ---------------------------------------------------------------------------

describe("buildGraphIndexes", () => {
	it("produces empty maps for an empty graph", () => {
		const indexes = buildGraphIndexes({
			workflow: {} as any,
			nodes: [],
			edges: [],
			status_transitions: [],
		});
		expect(indexes.taskToNode.size).toBe(0);
		expect(indexes.statusToTransition.size).toBe(0);
		expect(indexes.nodePairToEdge.size).toBe(0);
	});

	it("indexes nodes, transitions, and edges by their natural keys", () => {
		const indexes = buildGraphIndexes({
			workflow: {} as any,
			nodes: [makeNode("n1", "t1"), makeNode("n2", "t2")],
			edges: [makeEdge("e1", "n1", "n2")],
			status_transitions: [makeTransition("tr1", "s-ready", "s-done")],
		});
		expect(indexes.taskToNode.get("t1")).toBe("n1");
		expect(indexes.taskToNode.get("t2")).toBe("n2");
		expect(indexes.statusToTransition.get("s-ready")).toBe("tr1");
		expect(indexes.nodePairToEdge.get("n1|n2")).toBe("e1");
	});

	it("does not confuse an edge's reversed direction with a different key", () => {
		const indexes = buildGraphIndexes({
			workflow: {} as any,
			nodes: [makeNode("n1", "t1"), makeNode("n2", "t2")],
			edges: [makeEdge("e1", "n1", "n2")],
			status_transitions: [],
		});
		expect(indexes.nodePairToEdge.get("n1|n2")).toBe("e1");
		expect(indexes.nodePairToEdge.get("n2|n1")).toBeUndefined();
	});
});

describe("emptyGraphIndexes", () => {
	it("returns all-empty maps", () => {
		const indexes = emptyGraphIndexes();
		expect(indexes.taskToNode.size).toBe(0);
		expect(indexes.statusToTransition.size).toBe(0);
		expect(indexes.nodePairToEdge.size).toBe(0);
	});

	it("returns independent map instances on each call", () => {
		const a = emptyGraphIndexes();
		const b = emptyGraphIndexes();
		a.taskToNode.set("t1", "n1");
		expect(b.taskToNode.size).toBe(0);
	});
});

// ---------------------------------------------------------------------------
// dedupeByKey
// ---------------------------------------------------------------------------

describe("dedupeByKey", () => {
	it("returns an empty array unchanged", () => {
		expect(dedupeByKey<{ k: string }>([], (i) => i.k)).toEqual([]);
	});

	it("returns items untouched, in order, when there are no duplicate keys", () => {
		const items = [
			{ k: "a", v: 1 },
			{ k: "b", v: 2 },
			{ k: "c", v: 3 },
		];
		expect(dedupeByKey(items, (i) => i.k)).toEqual(items);
	});

	it("keeps only the last value for a repeated key, at that key's first position", () => {
		const items = [
			{ k: "a", v: 1 },
			{ k: "b", v: 2 },
			{ k: "a", v: 3 },
		];
		expect(dedupeByKey(items, (i) => i.k)).toEqual([
			{ k: "a", v: 3 },
			{ k: "b", v: 2 },
		]);
	});

	it("collapses every entry to the single last one when all keys match", () => {
		const items = [
			{ k: "a", v: 1 },
			{ k: "a", v: 2 },
			{ k: "a", v: 3 },
		];
		expect(dedupeByKey(items, (i) => i.k)).toEqual([{ k: "a", v: 3 }]);
	});
});

// ---------------------------------------------------------------------------
// extractApiErrorMessage
// ---------------------------------------------------------------------------

describe("extractApiErrorMessage", () => {
	it("extracts the error field from a realistic API error envelope", () => {
		const err = new Error(
			'API request failed: 409 Conflict - {"success":false,"error_code":"WORKFLOW_EDGE_CYCLE","error":"workflow: this edge would create a cycle","request_id":"abc"}',
		);
		expect(extractApiErrorMessage(err)).toBe(
			"workflow: this edge would create a cycle",
		);
	});

	it("returns the raw message when there is no JSON body at all", () => {
		const err = new Error("network request failed");
		expect(extractApiErrorMessage(err)).toBe("network request failed");
	});

	it("returns the full raw message when the text after the first brace isn't valid JSON", () => {
		const err = new Error("Something failed: {not valid json} trailing");
		expect(extractApiErrorMessage(err)).toBe(
			"Something failed: {not valid json} trailing",
		);
	});

	it("returns the full raw message when the JSON parses but has no string error field", () => {
		const err = new Error(
			'API request failed: 500 - {"success":false,"error_code":"UNKNOWN"}',
		);
		expect(extractApiErrorMessage(err)).toBe(
			'API request failed: 500 - {"success":false,"error_code":"UNKNOWN"}',
		);
	});

	it("returns the full raw message when the error field is not a string", () => {
		const err = new Error('failed - {"error":123}');
		expect(extractApiErrorMessage(err)).toBe('failed - {"error":123}');
	});

	it("falls back to String() for a non-Error thrown value", () => {
		expect(extractApiErrorMessage("plain string failure")).toBe(
			"plain string failure",
		);
	});
});

// ---------------------------------------------------------------------------
// applyNodes
// ---------------------------------------------------------------------------

describe("applyNodes", () => {
	describe("remove", () => {
		it("skips a taskId with no existing node, without calling the client", async () => {
			const client = makeClient();
			const indexes = emptyGraphIndexes();
			const { removed } = await applyNodes(
				makeCtx(client),
				undefined,
				["ghost"],
				indexes,
			);
			expect(client.removeWorkflowNode).not.toHaveBeenCalled();
			expect(removed).toEqual({
				items: [
					{
						key: "ghost",
						outcome: "skipped",
						detail: "no node exists for this taskId in the workflow",
					},
				],
				hasFailure: false,
			});
		});

		it("removes a node by taskId and deletes it from the index", async () => {
			const client = makeClient({
				removeWorkflowNode: vi.fn().mockResolvedValue(undefined),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			const { removed } = await applyNodes(
				makeCtx(client),
				undefined,
				["t1"],
				indexes,
			);
			expect(client.removeWorkflowNode).toHaveBeenCalledWith("p1", "wf1", "n1");
			expect(removed).toEqual({
				items: [{ key: "t1", outcome: "removed" }],
				hasFailure: false,
			});
			expect(indexes.taskToNode.has("t1")).toBe(false);
		});

		it("reports a failure and keeps the index entry when removal rejects", async () => {
			const client = makeClient({
				removeWorkflowNode: vi.fn().mockRejectedValue(new Error("boom")),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			const { removed } = await applyNodes(
				makeCtx(client),
				undefined,
				["t1"],
				indexes,
			);
			expect(removed).toEqual({
				items: [{ key: "t1", outcome: "failed", detail: "boom" }],
				hasFailure: true,
			});
			expect(indexes.taskToNode.get("t1")).toBe("n1");
		});

		it("attempts every taskId independently even if an earlier one fails", async () => {
			const client = makeClient({
				removeWorkflowNode: vi
					.fn()
					.mockImplementation((_p: string, _w: string, nodeId: string) =>
						nodeId === "n-bad"
							? Promise.reject(new Error("boom"))
							: Promise.resolve(undefined),
					),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("bad", "n-bad");
			indexes.taskToNode.set("good", "n-good");
			const { removed } = await applyNodes(
				makeCtx(client),
				undefined,
				["bad", "good"],
				indexes,
			);
			expect(client.removeWorkflowNode).toHaveBeenCalledTimes(2);
			expect(removed.items.map((i) => [i.key, i.outcome])).toEqual([
				["bad", "failed"],
				["good", "removed"],
			]);
		});

		it("issues removeWorkflowNode calls for every taskId concurrently rather than one at a time", async () => {
			let inFlight = 0;
			let maxInFlight = 0;
			const client = makeClient({
				removeWorkflowNode: vi.fn().mockImplementation(async () => {
					inFlight++;
					maxInFlight = Math.max(maxInFlight, inFlight);
					await new Promise((resolve) => setTimeout(resolve, 0));
					inFlight--;
				}),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			indexes.taskToNode.set("t3", "n3");
			await applyNodes(makeCtx(client), undefined, ["t1", "t2", "t3"], indexes);
			expect(maxInFlight).toBe(3);
		});

		it("de-duplicates a taskId repeated in remove, calling the client only once", async () => {
			const client = makeClient({
				removeWorkflowNode: vi.fn().mockResolvedValue(undefined),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			const { removed } = await applyNodes(
				makeCtx(client),
				undefined,
				["t1", "t1"],
				indexes,
			);
			expect(client.removeWorkflowNode).toHaveBeenCalledTimes(1);
			expect(removed).toEqual({
				items: [{ key: "t1", outcome: "removed" }],
				hasFailure: false,
			});
		});
	});

	describe("set", () => {
		it("creates a node when the taskId has none yet", async () => {
			const client = makeClient({
				addWorkflowNode: vi
					.fn()
					.mockResolvedValue(makeNode("n1", "t1", 10, 20)),
			});
			const indexes = emptyGraphIndexes();
			const { set } = await applyNodes(
				makeCtx(client),
				[{ taskId: "t1", posX: 10, posY: 20 }],
				undefined,
				indexes,
			);
			expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
				task_id: "t1",
				pos_x: 10,
				pos_y: 20,
			});
			expect(client.updateWorkflowNode).not.toHaveBeenCalled();
			expect(set).toEqual({
				items: [{ key: "t1", outcome: "created" }],
				hasFailure: false,
			});
			expect(indexes.taskToNode.get("t1")).toBe("n1");
		});

		it("repositions instead of re-creating when the taskId already has a node", async () => {
			const client = makeClient({
				updateWorkflowNode: vi
					.fn()
					.mockResolvedValue(makeNode("n1", "t1", 10, 20)),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			const { set } = await applyNodes(
				makeCtx(client),
				[{ taskId: "t1", posX: 10, posY: 20 }],
				undefined,
				indexes,
			);
			expect(client.updateWorkflowNode).toHaveBeenCalledWith(
				"p1",
				"wf1",
				"n1",
				{ pos_x: 10, pos_y: 20 },
			);
			expect(client.addWorkflowNode).not.toHaveBeenCalled();
			expect(set).toEqual({
				items: [{ key: "t1", outcome: "updated" }],
				hasFailure: false,
			});
			expect(indexes.taskToNode.get("t1")).toBe("n1");
		});

		it("reports a failure and leaves the index untouched when addWorkflowNode rejects", async () => {
			const client = makeClient({
				addWorkflowNode: vi.fn().mockRejectedValue(new Error("boom")),
			});
			const indexes = emptyGraphIndexes();
			const { set } = await applyNodes(
				makeCtx(client),
				[{ taskId: "t1", posX: 0, posY: 0 }],
				undefined,
				indexes,
			);
			expect(set).toEqual({
				items: [{ key: "t1", outcome: "failed", detail: "boom" }],
				hasFailure: true,
			});
			expect(indexes.taskToNode.has("t1")).toBe(false);
		});

		it("reports a failure and leaves the node id untouched when updateWorkflowNode rejects", async () => {
			const client = makeClient({
				updateWorkflowNode: vi.fn().mockRejectedValue(new Error("boom")),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			const { set } = await applyNodes(
				makeCtx(client),
				[{ taskId: "t1", posX: 5, posY: 5 }],
				undefined,
				indexes,
			);
			expect(set).toEqual({
				items: [{ key: "t1", outcome: "failed", detail: "boom" }],
				hasFailure: true,
			});
			expect(indexes.taskToNode.get("t1")).toBe("n1");
		});

		it("dedupes repeated entries for the same taskId, applying only the last", async () => {
			const client = makeClient({
				addWorkflowNode: vi
					.fn()
					.mockImplementation((_p: string, _w: string, input: any) =>
						Promise.resolve(makeNode("n1", input.task_id, input.pos_x)),
					),
			});
			const indexes = emptyGraphIndexes();
			await applyNodes(
				makeCtx(client),
				[
					{ taskId: "t1", posX: 0, posY: 0 },
					{ taskId: "t1", posX: 100, posY: 200 },
				],
				undefined,
				indexes,
			);
			expect(client.addWorkflowNode).toHaveBeenCalledTimes(1);
			expect(client.addWorkflowNode).toHaveBeenCalledWith("p1", "wf1", {
				task_id: "t1",
				pos_x: 100,
				pos_y: 200,
			});
		});

		it("attempts every item independently even if an earlier one fails", async () => {
			const client = makeClient({
				addWorkflowNode: vi
					.fn()
					.mockImplementation((_p: string, _w: string, input: any) =>
						input.task_id === "bad"
							? Promise.reject(new Error("boom"))
							: Promise.resolve(makeNode(`n-${input.task_id}`, input.task_id)),
					),
			});
			const indexes = emptyGraphIndexes();
			const { set } = await applyNodes(
				makeCtx(client),
				[
					{ taskId: "bad", posX: 0, posY: 0 },
					{ taskId: "good", posX: 400, posY: 0 },
				],
				undefined,
				indexes,
			);
			expect(client.addWorkflowNode).toHaveBeenCalledTimes(2);
			expect(set.items.map((i) => [i.key, i.outcome])).toEqual([
				["bad", "failed"],
				["good", "created"],
			]);
		});
	});

	it("processes removals before sets", async () => {
		const client = makeClient({
			removeWorkflowNode: vi.fn().mockResolvedValue(undefined),
			addWorkflowNode: vi.fn().mockResolvedValue(makeNode("n2", "t2")),
		});
		const indexes = emptyGraphIndexes();
		indexes.taskToNode.set("t1", "n1");
		await applyNodes(
			makeCtx(client),
			[{ taskId: "t2", posX: 0, posY: 0 }],
			["t1"],
			indexes,
		);
		const removeOrder = client.removeWorkflowNode.mock.invocationCallOrder[0];
		const addOrder = client.addWorkflowNode.mock.invocationCallOrder[0];
		expect(removeOrder).toBeLessThan(addOrder);
	});
});

// ---------------------------------------------------------------------------
// applyStatusTransitions
// ---------------------------------------------------------------------------

describe("applyStatusTransitions", () => {
	it("skips removing a statusId with no existing transition", async () => {
		const client = makeClient();
		const { removed } = await applyStatusTransitions(
			makeCtx(client),
			undefined,
			["s-ghost"],
			emptyGraphIndexes(),
		);
		expect(client.removeWorkflowStatusTransition).not.toHaveBeenCalled();
		expect(removed.items).toEqual([
			{
				key: "s-ghost",
				outcome: "skipped",
				detail: "no status transition exists for this statusId in the workflow",
			},
		]);
	});

	it("removes a transition by statusId and updates the index", async () => {
		const client = makeClient({
			removeWorkflowStatusTransition: vi.fn().mockResolvedValue(undefined),
		});
		const indexes = emptyGraphIndexes();
		indexes.statusToTransition.set("s-ready", "tr1");
		const { removed } = await applyStatusTransitions(
			makeCtx(client),
			undefined,
			["s-ready"],
			indexes,
		);
		expect(client.removeWorkflowStatusTransition).toHaveBeenCalledWith(
			"p1",
			"wf1",
			"tr1",
		);
		expect(removed.items).toEqual([{ key: "s-ready", outcome: "removed" }]);
		expect(indexes.statusToTransition.has("s-ready")).toBe(false);
	});

	it("keeps the index entry and reports failure when the removal rejects", async () => {
		const client = makeClient({
			removeWorkflowStatusTransition: vi
				.fn()
				.mockRejectedValue(new Error("nope")),
		});
		const indexes = emptyGraphIndexes();
		indexes.statusToTransition.set("s-ready", "tr1");
		const { removed } = await applyStatusTransitions(
			makeCtx(client),
			undefined,
			["s-ready"],
			indexes,
		);
		expect(removed.items).toEqual([
			{ key: "s-ready", outcome: "failed", detail: "nope" },
		]);
		expect(indexes.statusToTransition.get("s-ready")).toBe("tr1");
	});

	it("creates a transition and defaults a missing nextStatusId to null", async () => {
		const client = makeClient({
			setWorkflowStatusTransition: vi
				.fn()
				.mockResolvedValue(makeTransition("tr1", "s-done", null)),
		});
		const indexes = emptyGraphIndexes();
		const { set } = await applyStatusTransitions(
			makeCtx(client),
			[{ statusId: "s-done" }],
			undefined,
			indexes,
		);
		expect(client.setWorkflowStatusTransition).toHaveBeenCalledWith(
			"p1",
			"wf1",
			{ status_id: "s-done", next_status_id: null },
		);
		expect(set.items).toEqual([{ key: "s-done", outcome: "created" }]);
		expect(indexes.statusToTransition.get("s-done")).toBe("tr1");
	});

	it("passes an explicit nextStatusId through unchanged", async () => {
		const client = makeClient({
			setWorkflowStatusTransition: vi
				.fn()
				.mockResolvedValue(makeTransition("tr1", "s-ready", "s-done")),
		});
		await applyStatusTransitions(
			makeCtx(client),
			[{ statusId: "s-ready", nextStatusId: "s-done" }],
			undefined,
			emptyGraphIndexes(),
		);
		expect(client.setWorkflowStatusTransition).toHaveBeenCalledWith(
			"p1",
			"wf1",
			{ status_id: "s-ready", next_status_id: "s-done" },
		);
	});

	it("reports 'updated' (not 'created') when a transition already existed for that statusId", async () => {
		const client = makeClient({
			setWorkflowStatusTransition: vi
				.fn()
				.mockResolvedValue(makeTransition("tr1", "s-ready", "s-review")),
		});
		const indexes = emptyGraphIndexes();
		indexes.statusToTransition.set("s-ready", "tr1");
		const { set } = await applyStatusTransitions(
			makeCtx(client),
			[{ statusId: "s-ready", nextStatusId: "s-review" }],
			undefined,
			indexes,
		);
		expect(set.items).toEqual([{ key: "s-ready", outcome: "updated" }]);
	});

	it("leaves the index untouched and reports failure when setWorkflowStatusTransition rejects", async () => {
		const client = makeClient({
			setWorkflowStatusTransition: vi.fn().mockRejectedValue(new Error("bad")),
		});
		const indexes = emptyGraphIndexes();
		const { set } = await applyStatusTransitions(
			makeCtx(client),
			[{ statusId: "s-ready" }],
			undefined,
			indexes,
		);
		expect(set.items).toEqual([
			{ key: "s-ready", outcome: "failed", detail: "bad" },
		]);
		expect(indexes.statusToTransition.has("s-ready")).toBe(false);
	});

	it("dedupes repeated entries for the same statusId, applying only the last", async () => {
		const client = makeClient({
			setWorkflowStatusTransition: vi
				.fn()
				.mockImplementation((_p: string, _w: string, input: any) =>
					Promise.resolve(
						makeTransition("tr1", input.status_id, input.next_status_id),
					),
				),
		});
		await applyStatusTransitions(
			makeCtx(client),
			[
				{ statusId: "s-ready", nextStatusId: "s-a" },
				{ statusId: "s-ready", nextStatusId: "s-b" },
			],
			undefined,
			emptyGraphIndexes(),
		);
		expect(client.setWorkflowStatusTransition).toHaveBeenCalledTimes(1);
		expect(client.setWorkflowStatusTransition).toHaveBeenCalledWith(
			"p1",
			"wf1",
			{ status_id: "s-ready", next_status_id: "s-b" },
		);
	});
});

// ---------------------------------------------------------------------------
// applyEdges
// ---------------------------------------------------------------------------

describe("applyEdges", () => {
	describe("remove", () => {
		it("skips when the source task has no node", async () => {
			const client = makeClient();
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t2", "n2");
			const { removed } = await applyEdges(
				makeCtx(client),
				undefined,
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				indexes,
			);
			expect(client.removeWorkflowEdge).not.toHaveBeenCalled();
			expect(removed.items).toEqual([
				{
					key: "t1 -> t2",
					outcome: "skipped",
					detail:
						"one or both tasks have no node in this workflow — edge is already effectively gone",
				},
			]);
		});

		it("skips when the target task has no node", async () => {
			const client = makeClient();
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			const { removed } = await applyEdges(
				makeCtx(client),
				undefined,
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				indexes,
			);
			expect(client.removeWorkflowEdge).not.toHaveBeenCalled();
			expect(removed.items[0].outcome).toBe("skipped");
		});

		it("skips when both tasks have nodes but no edge exists between them", async () => {
			const client = makeClient();
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			const { removed } = await applyEdges(
				makeCtx(client),
				undefined,
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				indexes,
			);
			expect(client.removeWorkflowEdge).not.toHaveBeenCalled();
			expect(removed.items).toEqual([
				{
					key: "t1 -> t2",
					outcome: "skipped",
					detail: "no edge exists between these tasks",
				},
			]);
		});

		it("removes an existing edge by its resolved edge id and updates the index", async () => {
			const client = makeClient({
				removeWorkflowEdge: vi.fn().mockResolvedValue(undefined),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			indexes.nodePairToEdge.set("n1|n2", "e1");
			const { removed } = await applyEdges(
				makeCtx(client),
				undefined,
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				indexes,
			);
			expect(client.removeWorkflowEdge).toHaveBeenCalledWith("p1", "wf1", "e1");
			expect(removed.items).toEqual([{ key: "t1 -> t2", outcome: "removed" }]);
			expect(indexes.nodePairToEdge.has("n1|n2")).toBe(false);
		});

		it("keeps the index entry and reports failure when removal rejects", async () => {
			const client = makeClient({
				removeWorkflowEdge: vi.fn().mockRejectedValue(new Error("boom")),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			indexes.nodePairToEdge.set("n1|n2", "e1");
			const { removed } = await applyEdges(
				makeCtx(client),
				undefined,
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				indexes,
			);
			expect(removed.items).toEqual([
				{ key: "t1 -> t2", outcome: "failed", detail: "boom" },
			]);
			expect(indexes.nodePairToEdge.get("n1|n2")).toBe("e1");
		});

		it("issues removeWorkflowEdge calls for every edge concurrently rather than one at a time", async () => {
			let inFlight = 0;
			let maxInFlight = 0;
			const client = makeClient({
				removeWorkflowEdge: vi.fn().mockImplementation(async () => {
					inFlight++;
					maxInFlight = Math.max(maxInFlight, inFlight);
					await new Promise((resolve) => setTimeout(resolve, 0));
					inFlight--;
				}),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			indexes.taskToNode.set("t3", "n3");
			indexes.nodePairToEdge.set("n1|n2", "e1");
			indexes.nodePairToEdge.set("n2|n3", "e2");
			await applyEdges(
				makeCtx(client),
				undefined,
				[
					{ sourceTaskId: "t1", targetTaskId: "t2" },
					{ sourceTaskId: "t2", targetTaskId: "t3" },
				],
				indexes,
			);
			expect(maxInFlight).toBe(2);
		});

		it("de-duplicates a repeated edge ref in remove, calling the client only once", async () => {
			const client = makeClient({
				removeWorkflowEdge: vi.fn().mockResolvedValue(undefined),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			indexes.nodePairToEdge.set("n1|n2", "e1");
			const { removed } = await applyEdges(
				makeCtx(client),
				undefined,
				[
					{ sourceTaskId: "t1", targetTaskId: "t2" },
					{ sourceTaskId: "t1", targetTaskId: "t2" },
				],
				indexes,
			);
			expect(client.removeWorkflowEdge).toHaveBeenCalledTimes(1);
			expect(removed.items).toEqual([{ key: "t1 -> t2", outcome: "removed" }]);
		});
	});

	describe("add", () => {
		it("fails with the source taskId named when the source has no node", async () => {
			const client = makeClient();
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t2", "n2");
			const { added } = await applyEdges(
				makeCtx(client),
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				undefined,
				indexes,
			);
			expect(client.addWorkflowEdge).not.toHaveBeenCalled();
			expect(added.items).toEqual([
				{
					key: "t1 -> t2",
					outcome: "failed",
					detail:
						"taskId t1 has no node in this workflow — add it via nodes first",
				},
			]);
		});

		it("fails with the target taskId named when the target has no node", async () => {
			const client = makeClient();
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			const { added } = await applyEdges(
				makeCtx(client),
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				undefined,
				indexes,
			);
			expect(added.items).toEqual([
				{
					key: "t1 -> t2",
					outcome: "failed",
					detail:
						"taskId t2 has no node in this workflow — add it via nodes first",
				},
			]);
		});

		it("names the source taskId when both source and target are missing a node", async () => {
			const client = makeClient();
			const { added } = await applyEdges(
				makeCtx(client),
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				undefined,
				emptyGraphIndexes(),
			);
			expect(added.items[0].detail).toContain("taskId t1");
		});

		it("adds an edge once both tasks resolve to nodes and updates the index", async () => {
			const client = makeClient({
				addWorkflowEdge: vi.fn().mockResolvedValue(makeEdge("e1", "n1", "n2")),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			const { added } = await applyEdges(
				makeCtx(client),
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				undefined,
				indexes,
			);
			expect(client.addWorkflowEdge).toHaveBeenCalledWith("p1", "wf1", {
				source_node_id: "n1",
				target_node_id: "n2",
			});
			expect(added.items).toEqual([{ key: "t1 -> t2", outcome: "created" }]);
			expect(indexes.nodePairToEdge.get("n1|n2")).toBe("e1");
		});

		it("resolves an edge against a node added earlier in the same indexes object", async () => {
			// Simulates applyNodes having just populated the index within the
			// same create_workflow/update_workflow call.
			const client = makeClient({
				addWorkflowEdge: vi.fn().mockResolvedValue(makeEdge("e1", "n1", "n2")),
			});
			const indexes = emptyGraphIndexes();
			await applyNodes(
				makeCtx(
					makeClient({
						addWorkflowNode: vi.fn().mockResolvedValue(makeNode("n1", "t1")),
					}),
				),
				[{ taskId: "t1", posX: 0, posY: 0 }],
				undefined,
				indexes,
			);
			indexes.taskToNode.set("t2", "n2");
			await applyEdges(
				makeCtx(client),
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				undefined,
				indexes,
			);
			expect(client.addWorkflowEdge).toHaveBeenCalledWith("p1", "wf1", {
				source_node_id: "n1",
				target_node_id: "n2",
			});
		});

		it("leaves the index untouched and reports failure when addWorkflowEdge rejects", async () => {
			const client = makeClient({
				addWorkflowEdge: vi.fn().mockRejectedValue(new Error("would cycle")),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			const { added } = await applyEdges(
				makeCtx(client),
				[{ sourceTaskId: "t1", targetTaskId: "t2" }],
				undefined,
				indexes,
			);
			expect(added.items).toEqual([
				{ key: "t1 -> t2", outcome: "failed", detail: "would cycle" },
			]);
			expect(indexes.nodePairToEdge.has("n1|n2")).toBe(false);
		});

		it("dedupes repeated entries for the same source/target pair", async () => {
			const client = makeClient({
				addWorkflowEdge: vi.fn().mockResolvedValue(makeEdge("e1", "n1", "n2")),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("t2", "n2");
			await applyEdges(
				makeCtx(client),
				[
					{ sourceTaskId: "t1", targetTaskId: "t2" },
					{ sourceTaskId: "t1", targetTaskId: "t2" },
				],
				undefined,
				indexes,
			);
			expect(client.addWorkflowEdge).toHaveBeenCalledTimes(1);
		});

		it("attempts every edge independently even if an earlier one fails", async () => {
			const client = makeClient({
				addWorkflowEdge: vi
					.fn()
					.mockImplementation((_p: string, _w: string, input: any) =>
						input.target_node_id === "n-bad"
							? Promise.reject(new Error("cycle"))
							: Promise.resolve(
									makeEdge("e-ok", input.source_node_id, input.target_node_id),
								),
					),
			});
			const indexes = emptyGraphIndexes();
			indexes.taskToNode.set("t1", "n1");
			indexes.taskToNode.set("bad", "n-bad");
			indexes.taskToNode.set("good", "n-good");
			const { added } = await applyEdges(
				makeCtx(client),
				[
					{ sourceTaskId: "t1", targetTaskId: "bad" },
					{ sourceTaskId: "t1", targetTaskId: "good" },
				],
				undefined,
				indexes,
			);
			expect(client.addWorkflowEdge).toHaveBeenCalledTimes(2);
			expect(added.items.map((i) => i.outcome)).toEqual(["failed", "created"]);
		});
	});

	it("processes removals before additions", async () => {
		const client = makeClient({
			removeWorkflowEdge: vi.fn().mockResolvedValue(undefined),
			addWorkflowEdge: vi.fn().mockResolvedValue(makeEdge("e2", "n2", "n3")),
		});
		const indexes = emptyGraphIndexes();
		indexes.taskToNode.set("t1", "n1");
		indexes.taskToNode.set("t2", "n2");
		indexes.taskToNode.set("t3", "n3");
		indexes.nodePairToEdge.set("n1|n2", "e1");
		await applyEdges(
			makeCtx(client),
			[{ sourceTaskId: "t2", targetTaskId: "t3" }],
			[{ sourceTaskId: "t1", targetTaskId: "t2" }],
			indexes,
		);
		const removeOrder = client.removeWorkflowEdge.mock.invocationCallOrder[0];
		const addOrder = client.addWorkflowEdge.mock.invocationCallOrder[0];
		expect(removeOrder).toBeLessThan(addOrder);
	});
});
