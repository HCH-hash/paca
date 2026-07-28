import { queryOptions } from "@tanstack/react-query";

import { apiClient } from "./api-client";
import type { SuccessEnvelope } from "./api-error";

// ── Shapes ────────────────────────────────────────────────────────────────────

export type WorkflowStatus = "draft" | "active" | "archived";

export interface Workflow {
	id: string;
	project_id: string;
	name: string;
	description: string;
	status: WorkflowStatus;
	created_by?: string | null;
	created_at: string;
	updated_at: string;
}

export interface WorkflowStatusTransition {
	id: string;
	workflow_id: string;
	status_id: string;
	next_status_id?: string | null;
	created_at: string;
	updated_at: string;
}

export interface WorkflowNode {
	id: string;
	workflow_id: string;
	task_id: string;
	pos_x: number;
	pos_y: number;
	created_at: string;
	updated_at: string;
}

export interface WorkflowEdge {
	id: string;
	workflow_id: string;
	source_node_id: string;
	target_node_id: string;
	created_at: string;
}

export interface WorkflowGraph {
	workflow: Workflow;
	nodes: WorkflowNode[];
	edges: WorkflowEdge[];
	status_transitions: WorkflowStatusTransition[];
}

// ── Workflows ─────────────────────────────────────────────────────────────────

export async function listWorkflows(
	projectId: string,
	status?: WorkflowStatus,
): Promise<Workflow[]> {
	const { data } = await apiClient.instance.get<
		SuccessEnvelope<{ items: Workflow[] }>
	>(`/projects/${projectId}/workflows`, { params: status ? { status } : {} });
	return data.data.items;
}

export async function listWorkflowsForTask(
	projectId: string,
	taskId: string,
): Promise<Workflow[]> {
	const { data } = await apiClient.instance.get<
		SuccessEnvelope<{ items: Workflow[] }>
	>(`/projects/${projectId}/tasks/${taskId}/workflows`);
	return data.data.items;
}

export async function createWorkflow(
	projectId: string,
	payload: { name: string; description?: string },
): Promise<Workflow> {
	const { data } = await apiClient.instance.post<SuccessEnvelope<Workflow>>(
		`/projects/${projectId}/workflows`,
		payload,
	);
	return data.data;
}

export async function getWorkflow(
	projectId: string,
	workflowId: string,
): Promise<WorkflowGraph> {
	const { data } = await apiClient.instance.get<SuccessEnvelope<WorkflowGraph>>(
		`/projects/${projectId}/workflows/${workflowId}`,
	);
	return data.data;
}

export async function updateWorkflow(
	projectId: string,
	workflowId: string,
	payload: { name?: string; description?: string },
): Promise<Workflow> {
	const { data } = await apiClient.instance.patch<SuccessEnvelope<Workflow>>(
		`/projects/${projectId}/workflows/${workflowId}`,
		payload,
	);
	return data.data;
}

export async function deleteWorkflow(
	projectId: string,
	workflowId: string,
): Promise<void> {
	await apiClient.instance.delete(
		`/projects/${projectId}/workflows/${workflowId}`,
	);
}

export async function activateWorkflow(
	projectId: string,
	workflowId: string,
): Promise<Workflow> {
	const { data } = await apiClient.instance.post<SuccessEnvelope<Workflow>>(
		`/projects/${projectId}/workflows/${workflowId}/activate`,
	);
	return data.data;
}

export async function archiveWorkflow(
	projectId: string,
	workflowId: string,
): Promise<Workflow> {
	const { data } = await apiClient.instance.post<SuccessEnvelope<Workflow>>(
		`/projects/${projectId}/workflows/${workflowId}/archive`,
	);
	return data.data;
}

export async function revertWorkflowToDraft(
	projectId: string,
	workflowId: string,
): Promise<Workflow> {
	const { data } = await apiClient.instance.post<SuccessEnvelope<Workflow>>(
		`/projects/${projectId}/workflows/${workflowId}/revert-to-draft`,
	);
	return data.data;
}

// ── Nodes ─────────────────────────────────────────────────────────────────────

export async function addWorkflowNode(
	projectId: string,
	workflowId: string,
	payload: { task_id: string; pos_x: number; pos_y: number },
): Promise<WorkflowNode> {
	const { data } = await apiClient.instance.post<SuccessEnvelope<WorkflowNode>>(
		`/projects/${projectId}/workflows/${workflowId}/nodes`,
		payload,
	);
	return data.data;
}

export async function updateWorkflowNode(
	projectId: string,
	workflowId: string,
	nodeId: string,
	payload: { pos_x?: number; pos_y?: number },
): Promise<WorkflowNode> {
	const { data } = await apiClient.instance.patch<
		SuccessEnvelope<WorkflowNode>
	>(`/projects/${projectId}/workflows/${workflowId}/nodes/${nodeId}`, payload);
	return data.data;
}

export async function removeWorkflowNode(
	projectId: string,
	workflowId: string,
	nodeId: string,
): Promise<void> {
	await apiClient.instance.delete(
		`/projects/${projectId}/workflows/${workflowId}/nodes/${nodeId}`,
	);
}

// ── Status transitions ("status workflow") ───────────────────────────────────

export async function setWorkflowStatusTransition(
	projectId: string,
	workflowId: string,
	payload: { status_id: string; next_status_id: string | null },
): Promise<WorkflowStatusTransition> {
	const { data } = await apiClient.instance.post<
		SuccessEnvelope<WorkflowStatusTransition>
	>(
		`/projects/${projectId}/workflows/${workflowId}/status-transitions`,
		payload,
	);
	return data.data;
}

export async function removeWorkflowStatusTransition(
	projectId: string,
	workflowId: string,
	transitionId: string,
): Promise<void> {
	await apiClient.instance.delete(
		`/projects/${projectId}/workflows/${workflowId}/status-transitions/${transitionId}`,
	);
}

// ── Edges ─────────────────────────────────────────────────────────────────────

export async function addWorkflowEdge(
	projectId: string,
	workflowId: string,
	payload: { source_node_id: string; target_node_id: string },
): Promise<WorkflowEdge> {
	const { data } = await apiClient.instance.post<SuccessEnvelope<WorkflowEdge>>(
		`/projects/${projectId}/workflows/${workflowId}/edges`,
		payload,
	);
	return data.data;
}

export async function removeWorkflowEdge(
	projectId: string,
	workflowId: string,
	edgeId: string,
): Promise<void> {
	await apiClient.instance.delete(
		`/projects/${projectId}/workflows/${workflowId}/edges/${edgeId}`,
	);
}

// ── Query options ─────────────────────────────────────────────────────────────

export const workflowsQueryOptions = (
	projectId: string,
	status?: WorkflowStatus,
) =>
	queryOptions({
		queryKey: ["projects", projectId, "workflows", { status }],
		queryFn: () => listWorkflows(projectId, status),
		enabled: !!projectId,
	});

export const workflowQueryOptions = (projectId: string, workflowId: string) =>
	queryOptions({
		queryKey: ["projects", projectId, "workflows", workflowId],
		queryFn: () => getWorkflow(projectId, workflowId),
		enabled: !!projectId && !!workflowId,
	});

export const workflowsForTaskQueryOptions = (
	projectId: string,
	taskId: string,
) =>
	queryOptions({
		queryKey: ["projects", projectId, "tasks", taskId, "workflows"],
		queryFn: () => listWorkflowsForTask(projectId, taskId),
		enabled: !!projectId && !!taskId,
	});
