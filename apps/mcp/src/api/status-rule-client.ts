import type {
	CreateStatusAssignmentRuleInput,
	PacaConfig,
	StatusAssignmentRule,
	SuccessEnvelope,
	UpdateStatusAssignmentRuleInput,
} from "../types/index.js";

/**
 * API client for project-wide status-assignment-rule endpoints —
 * independent of any automation workflow.
 */
export class PacaAPIStatusRuleClient {
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

		return jsonResponse;
	}

	async listStatusRules(projectId: string): Promise<StatusAssignmentRule[]> {
		const response = await this.request(
			"GET",
			`/api/v1/projects/${projectId}/status-assignment-rules`,
		);
		if (Array.isArray(response)) {
			return response;
		}
		return response.items || response.data || [];
	}

	async createStatusRule(
		projectId: string,
		input: CreateStatusAssignmentRuleInput,
	): Promise<StatusAssignmentRule> {
		return this.request(
			"POST",
			`/api/v1/projects/${projectId}/status-assignment-rules`,
			input,
		);
	}

	async updateStatusRule(
		projectId: string,
		ruleId: string,
		input: UpdateStatusAssignmentRuleInput,
	): Promise<StatusAssignmentRule> {
		return this.request(
			"PATCH",
			`/api/v1/projects/${projectId}/status-assignment-rules/${ruleId}`,
			input,
		);
	}

	async deleteStatusRule(projectId: string, ruleId: string): Promise<void> {
		await this.request(
			"DELETE",
			`/api/v1/projects/${projectId}/status-assignment-rules/${ruleId}`,
		);
	}

	async reorderStatusRules(
		projectId: string,
		statusId: string,
		ruleIds: string[],
	): Promise<void> {
		await this.request(
			"PUT",
			`/api/v1/projects/${projectId}/status-assignment-rules/positions`,
			{ status_id: statusId, rule_ids: ruleIds },
		);
	}
}
