import type { PacaAPIWorkflowClient } from "../api/index.js";
import type { WorkflowGraph } from "../types/index.js";

/**
 * Shared apply-logic for `create_workflow`/`update_workflow`: both accept a
 * full graph (nodes/status transitions/edges) addressed by
 * taskId/statusId/task-pairs rather than internal node/transition/edge
 * UUIDs, and both need to translate that into the granular REST calls the
 * backend actually exposes. This module is that translation layer.
 *
 * Reassignment (status->assignee "rules") is NOT part of this graph anymore
 * — it's a separate, project-wide concern handled by the standalone
 * status_assignment_rule tools (see status-rule-tools.ts), independent of
 * any workflow.
 */

// --- Item input shapes (already Zod-validated by the caller) -----------------

export interface NodeSetInput {
	taskId: string;
	// Required, not optional: the agent is the only one who knows where a
	// node should sit, so it must always pick a position — see the
	// posX/posY tool-description guidance in workflow-tools.ts.
	posX: number;
	posY: number;
}

export interface StatusTransitionSetInput {
	statusId: string;
	nextStatusId?: string | null;
}

export interface EdgeRefInput {
	sourceTaskId: string;
	targetTaskId: string;
}

// --- Per-item / per-category results ------------------------------------------

export type ItemOutcome =
	| "created"
	| "updated"
	| "removed"
	| "skipped"
	| "failed";

export interface ItemResult {
	key: string;
	outcome: ItemOutcome;
	detail?: string;
}

export interface CategoryResult {
	items: ItemResult[];
	hasFailure: boolean;
}

function emptyCategoryResult(): CategoryResult {
	return { items: [], hasFailure: false };
}

function pushResult(result: CategoryResult, item: ItemResult): void {
	result.items.push(item);
	if (item.outcome === "failed") {
		result.hasFailure = true;
	}
}

export function anyHasFailure(
	...results: Array<CategoryResult | undefined>
): boolean {
	return results.some((r) => r?.hasFailure);
}

/**
 * Renders one category's results as a text block, or null if the category
 * wasn't touched at all (so callers can skip it from the final summary).
 */
export function formatCategoryResult(
	label: string,
	result: CategoryResult | undefined,
): string | null {
	if (!result || result.items.length === 0) {
		return null;
	}
	const counts: Record<ItemOutcome, number> = {
		created: 0,
		updated: 0,
		removed: 0,
		skipped: 0,
		failed: 0,
	};
	for (const item of result.items) {
		counts[item.outcome]++;
	}
	const parts: string[] = [];
	if (counts.created) parts.push(`${counts.created} created`);
	if (counts.updated) parts.push(`${counts.updated} updated`);
	if (counts.removed) parts.push(`${counts.removed} removed`);
	if (counts.skipped) parts.push(`${counts.skipped} skipped`);
	if (counts.failed) parts.push(`${counts.failed} failed`);

	const lines = [`${label}: ${parts.join(", ")}`];
	for (const item of result.items) {
		if (item.outcome === "failed" || item.outcome === "skipped") {
			lines.push(`  - ${item.outcome} (${item.key}): ${item.detail}`);
		}
	}
	return lines.join("\n");
}

// --- Graph indexes: taskId/statusId/task-pair -> internal id ------------------

export interface GraphIndexes {
	taskToNode: Map<string, string>;
	statusToTransition: Map<string, string>;
	nodePairToEdge: Map<string, string>;
}

function nodePairKey(sourceNodeId: string, targetNodeId: string): string {
	return `${sourceNodeId}|${targetNodeId}`;
}

/** Builds the natural-key -> internal-id lookup maps from a fetched graph. */
export function buildGraphIndexes(graph: WorkflowGraph): GraphIndexes {
	const taskToNode = new Map<string, string>();
	for (const n of graph.nodes) {
		taskToNode.set(n.task_id, n.id);
	}
	const statusToTransition = new Map<string, string>();
	for (const t of graph.status_transitions) {
		statusToTransition.set(t.status_id, t.id);
	}
	const nodePairToEdge = new Map<string, string>();
	for (const e of graph.edges) {
		nodePairToEdge.set(nodePairKey(e.source_node_id, e.target_node_id), e.id);
	}
	return { taskToNode, statusToTransition, nodePairToEdge };
}

/** Empty indexes for contexts with no prior graph (e.g. a brand-new workflow). */
export function emptyGraphIndexes(): GraphIndexes {
	return {
		taskToNode: new Map(),
		statusToTransition: new Map(),
		nodePairToEdge: new Map(),
	};
}

/** Keeps only the last entry per key — "last write wins" for a request array. */
export function dedupeByKey<T>(items: T[], keyFn: (item: T) => string): T[] {
	const map = new Map<string, T>();
	for (const item of items) {
		map.set(keyFn(item), item);
	}
	return [...map.values()];
}

/**
 * Pulls the human-readable `error` field out of the JSON body embedded in
 * the Error thrown by PacaAPIWorkflowClient's request() helper (formatted as
 * `API request failed: <status> <statusText> - <jsonBody>`), falling back to
 * the raw message if the body isn't parseable JSON with that shape.
 */
export function extractApiErrorMessage(err: unknown): string {
	const message = err instanceof Error ? err.message : String(err);
	const jsonStart = message.indexOf("{");
	if (jsonStart === -1) {
		return message;
	}
	try {
		const parsed = JSON.parse(message.slice(jsonStart));
		if (parsed && typeof parsed.error === "string") {
			return parsed.error;
		}
	} catch {
		// Not JSON (or not the expected shape) — fall through to raw message.
	}
	return message;
}

export interface OrchestrationContext {
	client: PacaAPIWorkflowClient;
	projectId: string;
	workflowId: string;
}

// --- Nodes ---------------------------------------------------------------------

// Minimum canvas spacing the agent is asked to use between any two nodes.
// Defined once here and interpolated into both the tool-description text
// (workflow-tools.ts) and checkNodeSpacing below, so the two can never drift
// out of sync — X in particular has been adjusted several times based on
// live feedback (400 -> 600 -> 900 -> 1800 -> back down to 400 as too-wide,
// halved to 200 with Y, then back up to 300); centralizing it means each
// adjustment is one constant, not a hunt across strings. Canvas cards are a
// fixed 256px wide (see apps/web's `w-64` TaskNode in workflow-canvas.tsx).
export const RECOMMENDED_NODE_GAP_X = 300;
export const RECOMMENDED_NODE_GAP_Y = 200;

export interface PositionedNode {
	taskId: string;
	posX: number;
	posY: number;
}

/**
 * Warns (without changing anything) when any two of `positions` are closer
 * than the recommended gap on BOTH axes at once — i.e. they'd read as
 * crowded/overlapping on the canvas rather than as clearly separate nodes.
 * Purely advisory: prose-only spacing guidance in the tool description was
 * not enough on its own (agents kept picking small, evenly-spaced values
 * like 200px regardless of the stated minimum), so this gives concrete,
 * per-call feedback the agent can act on — but it never moves a node itself;
 * position is still entirely the agent's call (see NodeSetInput).
 */
export function checkNodeSpacing(positions: PositionedNode[]): string | null {
	const violations: Array<{
		a: PositionedNode;
		b: PositionedNode;
		dx: number;
		dy: number;
	}> = [];
	for (let i = 0; i < positions.length; i++) {
		for (let j = i + 1; j < positions.length; j++) {
			const a = positions[i];
			const b = positions[j];
			const dx = Math.abs(a.posX - b.posX);
			const dy = Math.abs(a.posY - b.posY);
			if (dx < RECOMMENDED_NODE_GAP_X && dy < RECOMMENDED_NODE_GAP_Y) {
				violations.push({ a, b, dx, dy });
			}
		}
	}
	if (violations.length === 0) {
		return null;
	}

	violations.sort((v1, v2) => v1.dx + v1.dy - (v2.dx + v2.dy));
	const examples = violations
		.slice(0, 3)
		.map(
			(v) =>
				`  - ${v.a.taskId} (${v.a.posX}, ${v.a.posY}) and ${v.b.taskId} (${v.b.posX}, ${v.b.posY}) — ${v.dx}px apart horizontally, ${v.dy}px apart vertically`,
		);
	const more =
		violations.length > 3
			? `\n  ...and ${violations.length - 3} more pair(s) this close.`
			: "";
	return (
		`Warning: ${violations.length} pair(s) of these nodes are close enough to visually crowd or overlap on the canvas (need at least ${RECOMMENDED_NODE_GAP_X}px apart horizontally OR ${RECOMMENDED_NODE_GAP_Y}px apart vertically — not both less):\n` +
		`${examples.join("\n")}${more}\n` +
		"These positions were still applied as requested — spread them out further yourself (e.g. call update_workflow again with larger posX/posY differences) if this isn't what you intended."
	);
}

export async function applyNodes(
	ctx: OrchestrationContext,
	set: NodeSetInput[] | undefined,
	remove: string[] | undefined,
	indexes: GraphIndexes,
): Promise<{ removed: CategoryResult; set: CategoryResult }> {
	const { client, projectId, workflowId } = ctx;

	// Each item below targets a distinct taskId (remove is de-duplicated via
	// Set, set via dedupeByKey), so every call reads/writes a different
	// indexes.taskToNode entry — safe to fire concurrently instead of
	// paying for each REST round-trip sequentially.
	const removedResult = emptyCategoryResult();
	await Promise.all(
		[...new Set(remove ?? [])].map(async (taskId) => {
			const nodeId = indexes.taskToNode.get(taskId);
			if (!nodeId) {
				pushResult(removedResult, {
					key: taskId,
					outcome: "skipped",
					detail: "no node exists for this taskId in the workflow",
				});
				return;
			}
			try {
				await client.removeWorkflowNode(projectId, workflowId, nodeId);
				indexes.taskToNode.delete(taskId);
				pushResult(removedResult, { key: taskId, outcome: "removed" });
			} catch (err) {
				pushResult(removedResult, {
					key: taskId,
					outcome: "failed",
					detail: extractApiErrorMessage(err),
				});
			}
		}),
	);

	const setResult = emptyCategoryResult();
	await Promise.all(
		dedupeByKey(set ?? [], (i) => i.taskId).map(async (item) => {
			const existingNodeId = indexes.taskToNode.get(item.taskId);
			try {
				if (existingNodeId) {
					await client.updateWorkflowNode(
						projectId,
						workflowId,
						existingNodeId,
						{ pos_x: item.posX, pos_y: item.posY },
					);
					pushResult(setResult, { key: item.taskId, outcome: "updated" });
				} else {
					const node = await client.addWorkflowNode(projectId, workflowId, {
						task_id: item.taskId,
						pos_x: item.posX,
						pos_y: item.posY,
					});
					indexes.taskToNode.set(item.taskId, node.id);
					pushResult(setResult, { key: item.taskId, outcome: "created" });
				}
			} catch (err) {
				pushResult(setResult, {
					key: item.taskId,
					outcome: "failed",
					detail: extractApiErrorMessage(err),
				});
			}
		}),
	);

	return { removed: removedResult, set: setResult };
}

// --- Status transitions ------------------------------------------------------------

export async function applyStatusTransitions(
	ctx: OrchestrationContext,
	set: StatusTransitionSetInput[] | undefined,
	remove: string[] | undefined,
	indexes: GraphIndexes,
): Promise<{ removed: CategoryResult; set: CategoryResult }> {
	const { client, projectId, workflowId } = ctx;

	// See applyNodes: each item targets a distinct statusId, so these run
	// concurrently instead of one REST round-trip at a time.
	const removedResult = emptyCategoryResult();
	await Promise.all(
		[...new Set(remove ?? [])].map(async (statusId) => {
			const transitionId = indexes.statusToTransition.get(statusId);
			if (!transitionId) {
				pushResult(removedResult, {
					key: statusId,
					outcome: "skipped",
					detail:
						"no status transition exists for this statusId in the workflow",
				});
				return;
			}
			try {
				await client.removeWorkflowStatusTransition(
					projectId,
					workflowId,
					transitionId,
				);
				indexes.statusToTransition.delete(statusId);
				pushResult(removedResult, { key: statusId, outcome: "removed" });
			} catch (err) {
				pushResult(removedResult, {
					key: statusId,
					outcome: "failed",
					detail: extractApiErrorMessage(err),
				});
			}
		}),
	);

	const setResult = emptyCategoryResult();
	await Promise.all(
		dedupeByKey(set ?? [], (i) => i.statusId).map(async (item) => {
			const existed = indexes.statusToTransition.has(item.statusId);
			try {
				const transition = await client.setWorkflowStatusTransition(
					projectId,
					workflowId,
					{
						status_id: item.statusId,
						next_status_id: item.nextStatusId ?? null,
					},
				);
				indexes.statusToTransition.set(item.statusId, transition.id);
				pushResult(setResult, {
					key: item.statusId,
					outcome: existed ? "updated" : "created",
				});
			} catch (err) {
				pushResult(setResult, {
					key: item.statusId,
					outcome: "failed",
					detail: extractApiErrorMessage(err),
				});
			}
		}),
	);

	return { removed: removedResult, set: setResult };
}

// --- Edges -----------------------------------------------------------------------

function edgeLabel(ref: EdgeRefInput): string {
	return `${ref.sourceTaskId} -> ${ref.targetTaskId}`;
}

export async function applyEdges(
	ctx: OrchestrationContext,
	add: EdgeRefInput[] | undefined,
	remove: EdgeRefInput[] | undefined,
	indexes: GraphIndexes,
): Promise<{ removed: CategoryResult; added: CategoryResult }> {
	const { client, projectId, workflowId } = ctx;

	// See applyNodes: each item targets a distinct node pair (both add and
	// remove are de-duplicated via dedupeByKey), so these run concurrently
	// instead of one REST round-trip at a time.
	const removedResult = emptyCategoryResult();
	await Promise.all(
		dedupeByKey(remove ?? [], edgeLabel).map(async (ref) => {
			const sourceNodeId = indexes.taskToNode.get(ref.sourceTaskId);
			const targetNodeId = indexes.taskToNode.get(ref.targetTaskId);
			if (!sourceNodeId || !targetNodeId) {
				pushResult(removedResult, {
					key: edgeLabel(ref),
					outcome: "skipped",
					detail:
						"one or both tasks have no node in this workflow — edge is already effectively gone",
				});
				return;
			}
			const edgeId = indexes.nodePairToEdge.get(
				nodePairKey(sourceNodeId, targetNodeId),
			);
			if (!edgeId) {
				pushResult(removedResult, {
					key: edgeLabel(ref),
					outcome: "skipped",
					detail: "no edge exists between these tasks",
				});
				return;
			}
			try {
				await client.removeWorkflowEdge(projectId, workflowId, edgeId);
				indexes.nodePairToEdge.delete(nodePairKey(sourceNodeId, targetNodeId));
				pushResult(removedResult, { key: edgeLabel(ref), outcome: "removed" });
			} catch (err) {
				pushResult(removedResult, {
					key: edgeLabel(ref),
					outcome: "failed",
					detail: extractApiErrorMessage(err),
				});
			}
		}),
	);

	const addedResult = emptyCategoryResult();
	await Promise.all(
		dedupeByKey(add ?? [], edgeLabel).map(async (ref) => {
			const sourceNodeId = indexes.taskToNode.get(ref.sourceTaskId);
			const targetNodeId = indexes.taskToNode.get(ref.targetTaskId);
			if (!sourceNodeId || !targetNodeId) {
				const missing = !sourceNodeId ? ref.sourceTaskId : ref.targetTaskId;
				pushResult(addedResult, {
					key: edgeLabel(ref),
					outcome: "failed",
					detail: `taskId ${missing} has no node in this workflow — add it via nodes first`,
				});
				return;
			}
			try {
				const edge = await client.addWorkflowEdge(projectId, workflowId, {
					source_node_id: sourceNodeId,
					target_node_id: targetNodeId,
				});
				indexes.nodePairToEdge.set(
					nodePairKey(sourceNodeId, targetNodeId),
					edge.id,
				);
				pushResult(addedResult, { key: edgeLabel(ref), outcome: "created" });
			} catch (err) {
				pushResult(addedResult, {
					key: edgeLabel(ref),
					outcome: "failed",
					detail: extractApiErrorMessage(err),
				});
			}
		}),
	);

	return { removed: removedResult, added: addedResult };
}
