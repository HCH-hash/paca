import { queryOptions } from "@tanstack/react-query";

import { apiClient } from "./api-client";
import type { SuccessEnvelope } from "./api-error";

// ── Shapes ────────────────────────────────────────────────────────────────────

export interface IntRange {
	min: number;
	max: number;
}

export interface CustomFieldFilterSpec {
	values?: string[];
	min?: number;
	max?: number;
	after?: string;
	before?: string;
	contains?: string;
}

// TaskFilterSpec mirrors the backend's statusruledom.TaskFilterSpec exactly
// — the same shape is used for both request and response bodies. An empty
// object matches every task at the rule's trigger status.
export interface TaskFilterSpec {
	task_type_ids?: string[];
	sprint_ids?: string[];
	backlog_only?: boolean;
	assignee_ids?: string[];
	assignee_null?: boolean;
	tags?: string[];
	importance_ranges?: IntRange[];
	story_points_min?: number;
	story_points_max?: number;
	start_date_after?: string;
	start_date_before?: string;
	due_date_after?: string;
	due_date_before?: string;
	custom_fields?: Record<string, CustomFieldFilterSpec>;
}

export function isFilterEmpty(filter: TaskFilterSpec): boolean {
	return (
		!filter.task_type_ids?.length &&
		!filter.sprint_ids?.length &&
		!filter.backlog_only &&
		!filter.assignee_ids?.length &&
		!filter.assignee_null &&
		!filter.tags?.length &&
		!filter.importance_ranges?.length &&
		filter.story_points_min == null &&
		filter.story_points_max == null &&
		!filter.start_date_after &&
		!filter.start_date_before &&
		!filter.due_date_after &&
		!filter.due_date_before &&
		Object.keys(filter.custom_fields ?? {}).length === 0
	);
}

// filterSummaryCount returns how many individual scoping criteria a filter
// has set, for a compact "N filters" / "All tasks" badge.
export function filterSummaryCount(filter: TaskFilterSpec): number {
	let count = 0;
	if (filter.task_type_ids?.length) count++;
	if (filter.sprint_ids?.length || filter.backlog_only) count++;
	if (filter.assignee_ids?.length || filter.assignee_null) count++;
	if (filter.tags?.length) count++;
	if (filter.importance_ranges?.length) count++;
	if (filter.story_points_min != null || filter.story_points_max != null)
		count++;
	if (
		filter.start_date_after ||
		filter.start_date_before ||
		filter.due_date_after ||
		filter.due_date_before
	)
		count++;
	count += Object.keys(filter.custom_fields ?? {}).length;
	return count;
}

export interface StatusAssignmentRule {
	id: string;
	project_id: string;
	name: string;
	status_id: string;
	assignee_member_id: string;
	filter: TaskFilterSpec;
	priority: number;
	enabled: boolean;
	created_by?: string | null;
	created_at: string;
	updated_at: string;
}

export interface CreateStatusRuleInput {
	name: string;
	status_id: string;
	assignee_member_id: string;
	filter: TaskFilterSpec;
	enabled?: boolean;
}

export interface UpdateStatusRuleInput {
	name?: string;
	assignee_member_id?: string;
	filter?: TaskFilterSpec;
	enabled?: boolean;
}

// ── API calls ─────────────────────────────────────────────────────────────────

export async function listStatusRules(
	projectId: string,
): Promise<StatusAssignmentRule[]> {
	const { data } = await apiClient.instance.get<
		SuccessEnvelope<{ items: StatusAssignmentRule[] }>
	>(`/projects/${projectId}/status-assignment-rules`);
	return data.data.items;
}

export async function createStatusRule(
	projectId: string,
	payload: CreateStatusRuleInput,
): Promise<StatusAssignmentRule> {
	const { data } = await apiClient.instance.post<
		SuccessEnvelope<StatusAssignmentRule>
	>(`/projects/${projectId}/status-assignment-rules`, payload);
	return data.data;
}

export async function updateStatusRule(
	projectId: string,
	ruleId: string,
	payload: UpdateStatusRuleInput,
): Promise<StatusAssignmentRule> {
	const { data } = await apiClient.instance.patch<
		SuccessEnvelope<StatusAssignmentRule>
	>(`/projects/${projectId}/status-assignment-rules/${ruleId}`, payload);
	return data.data;
}

export async function deleteStatusRule(
	projectId: string,
	ruleId: string,
): Promise<void> {
	await apiClient.instance.delete(
		`/projects/${projectId}/status-assignment-rules/${ruleId}`,
	);
}

export async function reorderStatusRules(
	projectId: string,
	statusId: string,
	ruleIds: string[],
): Promise<void> {
	await apiClient.instance.put(
		`/projects/${projectId}/status-assignment-rules/positions`,
		{ status_id: statusId, rule_ids: ruleIds },
	);
}

// ── Query options ─────────────────────────────────────────────────────────────

export const statusRulesQueryOptions = (projectId: string) =>
	queryOptions({
		queryKey: ["projects", projectId, "status-assignment-rules"],
		queryFn: () => listStatusRules(projectId),
		enabled: !!projectId,
	});
