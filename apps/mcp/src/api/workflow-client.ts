import type {
	AddWorkflowEdgeInput,
	AddWorkflowNodeInput,
	CreateWorkflowInput,
	PacaConfig,
	SetWorkflowStatusTransitionInput,
	SuccessEnvelope,
	UpdateWorkflowInput,
	UpdateWorkflowNodeInput,
	Workflow,
	WorkflowEdge,
	WorkflowGraph,
	WorkflowNode,
	WorkflowStatus,
	WorkflowStatusTransition,
} from "../types/index.js";

/**
 * API client for automation-workflow endpoints.
 */
export class PacaAPIWorkflowClient {
	private config: PacaConfig;

	constructor(config: PacaConfig) {
		this.config = config;
	}

	private async request(
		method: string,
		path: string,
		body?: any,
	): Promise<any> {
		const url = `${this.config.baseURL}${path}`;
		const headers: Record<string, string> = {
			"Content-Type": "application/json",
			"X-API-Key": this.config.apiKey,
		};
		if (this.config.agentId) {
			headers["X-Agent-ID"] = this.config.agentId;
		}

		const options: RequestInit = {
			method,
			headers,
		};

		if (body) {
			options.body = JSON.stringify(body);
		}

		const response = await fetch(url, options);

		if (!response.ok) {
			const errorText = await response.text();
			throw new Error(
				`API request failed: ${response.status} ${response.statusText} - ${errorText}`,
			);
		}

		if (response.status === 204) {
			return undefined;
		}

		const jsonResponse = await response.json();

		// Handle SuccessEnvelope wrapper
		if (
			jsonResponse &&
			typeof jsonResponse === "object" &&
			"success" in jsonResponse
		) {
			const envelope = jsonResponse as SuccessEnvelope<any>;
			if (envelope.success) {
				return envelope.data;
			}
		}

		// Fallback for responses not wrapped in SuccessEnvelope
		return jsonResponse;
	}

	private async get(path: string): Promise<any> {
		return this.request("GET", path);
	}

	private async post(path: string, body?: any): Promise<any> {
		return this.request("POST", path, body);
	}

	private async patch(path: string, body: any): Promise<any> {
		return this.request("PATCH", path, body);
	}

	private async delete(path: string): Promise<any> {
		return this.request("DELETE", path);
	}

	// ==================== Workflows ====================

	async listWorkflows(
		projectId: string,
		status?: WorkflowStatus,
	): Promise<Workflow[]> {
		const queryString = status ? `?status=${status}` : "";
		const response = await this.get(
			`/api/v1/projects/${projectId}/workflows${queryString}`,
		);
		if (Array.isArray(response)) {
			return response;
		}
		return response.items || response.data || [];
	}

	async getWorkflow(
		projectId: string,
		workflowId: string,
	): Promise<WorkflowGraph> {
		return this.get(`/api/v1/projects/${projectId}/workflows/${workflowId}`);
	}

	async createWorkflow(
		projectId: string,
		input: CreateWorkflowInput,
	): Promise<Workflow> {
		return this.post(`/api/v1/projects/${projectId}/workflows`, input);
	}

	async updateWorkflow(
		projectId: string,
		workflowId: string,
		input: UpdateWorkflowInput,
	): Promise<Workflow> {
		return this.patch(
			`/api/v1/projects/${projectId}/workflows/${workflowId}`,
			input,
		);
	}

	async deleteWorkflow(projectId: string, workflowId: string): Promise<void> {
		await this.delete(`/api/v1/projects/${projectId}/workflows/${workflowId}`);
	}

	async activateWorkflow(
		projectId: string,
		workflowId: string,
	): Promise<Workflow> {
		return this.post(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/activate`,
		);
	}

	async archiveWorkflow(
		projectId: string,
		workflowId: string,
	): Promise<Workflow> {
		return this.post(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/archive`,
		);
	}

	async revertWorkflowToDraft(
		projectId: string,
		workflowId: string,
	): Promise<Workflow> {
		return this.post(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/revert-to-draft`,
		);
	}

	// ==================== Workflow Nodes ====================

	async addWorkflowNode(
		projectId: string,
		workflowId: string,
		input: AddWorkflowNodeInput,
	): Promise<WorkflowNode> {
		return this.post(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/nodes`,
			input,
		);
	}

	async updateWorkflowNode(
		projectId: string,
		workflowId: string,
		nodeId: string,
		input: UpdateWorkflowNodeInput,
	): Promise<WorkflowNode> {
		return this.patch(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/nodes/${nodeId}`,
			input,
		);
	}

	async removeWorkflowNode(
		projectId: string,
		workflowId: string,
		nodeId: string,
	): Promise<void> {
		await this.delete(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/nodes/${nodeId}`,
		);
	}

	// ==================== Workflow Status Transitions ====================

	async setWorkflowStatusTransition(
		projectId: string,
		workflowId: string,
		input: SetWorkflowStatusTransitionInput,
	): Promise<WorkflowStatusTransition> {
		return this.post(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/status-transitions`,
			input,
		);
	}

	async removeWorkflowStatusTransition(
		projectId: string,
		workflowId: string,
		transitionId: string,
	): Promise<void> {
		await this.delete(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/status-transitions/${transitionId}`,
		);
	}

	// ==================== Workflow Edges ====================

	async addWorkflowEdge(
		projectId: string,
		workflowId: string,
		input: AddWorkflowEdgeInput,
	): Promise<WorkflowEdge> {
		return this.post(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/edges`,
			input,
		);
	}

	async removeWorkflowEdge(
		projectId: string,
		workflowId: string,
		edgeId: string,
	): Promise<void> {
		await this.delete(
			`/api/v1/projects/${projectId}/workflows/${workflowId}/edges/${edgeId}`,
		);
	}
}
