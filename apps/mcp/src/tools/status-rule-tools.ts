import type { Tool } from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";
import type { PacaAPIStatusRuleClient } from "../api/index.js";
import type { StatusAssignmentRule, TaskFilterSpec } from "../types/index.js";
import { formatList } from "../utils/index.js";

const CustomFieldFilterSpecSchema = z.object({
	values: z.array(z.string()).optional(),
	min: z.number().optional(),
	max: z.number().optional(),
	after: z.string().optional(),
	before: z.string().optional(),
	contains: z.string().optional(),
});

const TaskFilterSpecSchema = z.object({
	task_type_ids: z.array(z.string()).optional(),
	sprint_ids: z.array(z.string()).optional(),
	backlog_only: z.boolean().optional(),
	assignee_ids: z.array(z.string()).optional(),
	assignee_null: z.boolean().optional(),
	tags: z.array(z.string()).optional(),
	importance_ranges: z
		.array(z.object({ min: z.number(), max: z.number() }))
		.optional(),
	story_points_min: z.number().optional(),
	story_points_max: z.number().optional(),
	start_date_after: z.string().optional(),
	start_date_before: z.string().optional(),
	due_date_after: z.string().optional(),
	due_date_before: z.string().optional(),
	custom_fields: z.record(z.string(), CustomFieldFilterSpecSchema).optional(),
});

const filterDescription =
	"Optional filter narrowing which tasks this rule applies to, matched against ALL of the given criteria (AND'd together). Omit entirely, or pass {}, to apply the rule to every task that reaches statusId. Fields: task_type_ids, sprint_ids, backlog_only (true = only tasks with no sprint), assignee_ids, assignee_null (true = only unassigned tasks), tags, importance_ranges ([{min,max}] importance bands, OR'd together), story_points_min/max, start_date_after/before, due_date_after/before (YYYY-MM-DD), and custom_fields (an object keyed by the custom field's field_key, each entry using values for select/multi_select/boolean fields, min/max for number fields, after/before for date fields, or contains for text/url fields).";

const ListStatusRulesSchema = z.object({
	projectId: z.string(),
});

const CreateStatusRuleSchema = z.object({
	projectId: z.string(),
	name: z.string(),
	statusId: z.string(),
	assigneeMemberId: z.string(),
	filter: TaskFilterSpecSchema.optional(),
	enabled: z.boolean().optional(),
});

const UpdateStatusRuleSchema = z.object({
	projectId: z.string(),
	ruleId: z.string(),
	name: z.string().optional(),
	assigneeMemberId: z.string().optional(),
	filter: TaskFilterSpecSchema.optional(),
	enabled: z.boolean().optional(),
});

const DeleteStatusRuleSchema = z.object({
	projectId: z.string(),
	ruleId: z.string(),
});

const ReorderStatusRulesSchema = z.object({
	projectId: z.string(),
	statusId: z.string(),
	ruleIds: z.array(z.string()),
});

/**
 * Returns all status-assignment-rule MCP tools — project-wide, filterable
 * status->assignee automation, independent of any automation workflow.
 */
export function getStatusRuleTools(): Tool[] {
	return [
		{
			name: "list_status_assignment_rules",
			description:
				"List all status-assignment rules in a project, across every trigger status. Each status's rules are evaluated in priority order (lower first); the first enabled rule whose filter matches a task wins.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
				},
				required: ["projectId"],
			},
		},
		{
			name: "create_status_assignment_rule",
			description:
				"Create a project-wide rule that automatically assigns a task to a member whenever it reaches a given status — for ANY task in the project, not just tasks in a specific automation workflow. Optionally scope it to matching tasks with a filter (task type, sprint, tags, importance, story points, dates, or custom fields). New rules are appended to the end of that status's priority list.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					name: {
						type: "string",
						description: "A short, human-readable name for the rule.",
					},
					statusId: {
						type: "string",
						description:
							"The trigger status UUID — the rule fires whenever a matching task reaches this status. Use list_task_statuses to get status IDs. Immutable after creation.",
					},
					assigneeMemberId: {
						type: "string",
						description:
							"The project_members.id to assign matching tasks to. Use list_project_members to get member IDs.",
					},
					filter: {
						type: "object",
						description: filterDescription,
					},
					enabled: {
						type: "boolean",
						description: "Whether the rule is active. Defaults to true.",
					},
				},
				required: ["projectId", "name", "statusId", "assigneeMemberId"],
			},
		},
		{
			name: "update_status_assignment_rule",
			description:
				"Update an existing status-assignment rule's name, assignee, filter, or enabled flag. The trigger status cannot be changed — delete and recreate the rule instead.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					ruleId: {
						type: "string",
						description:
							"The technical UUID of the rule. Use list_status_assignment_rules to get rule IDs.",
					},
					name: { type: "string", description: "The new name." },
					assigneeMemberId: {
						type: "string",
						description: "The new assignee's project_members.id.",
					},
					filter: {
						type: "object",
						description: `The new filter, replacing the existing one entirely. ${filterDescription}`,
					},
					enabled: {
						type: "boolean",
						description: "Enable or disable the rule without deleting it.",
					},
				},
				required: ["projectId", "ruleId"],
			},
		},
		{
			name: "delete_status_assignment_rule",
			description: "Delete a status-assignment rule.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					ruleId: {
						type: "string",
						description:
							"The technical UUID of the rule. Use list_status_assignment_rules to get rule IDs.",
					},
				},
				required: ["projectId", "ruleId"],
			},
		},
		{
			name: "reorder_status_assignment_rules",
			description:
				"Set the evaluation priority of every rule targeting one trigger status, in the given order (first = highest priority = evaluated first). ruleIds must be exactly that status's existing rule IDs (in the new desired order) — use list_status_assignment_rules first to get the current set.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					statusId: {
						type: "string",
						description: "The trigger status whose rules are being reordered.",
					},
					ruleIds: {
						type: "array",
						items: { type: "string" },
						description:
							"Every rule ID currently targeting statusId, in the desired priority order.",
					},
				},
				required: ["projectId", "statusId", "ruleIds"],
			},
		},
	];
}

function formatFilter(filter: TaskFilterSpec | undefined): string {
	if (!filter || Object.keys(filter).length === 0) {
		return "(none — applies to every task at this status)";
	}
	return JSON.stringify(filter);
}

function formatStatusRule(rule: StatusAssignmentRule): string {
	return `Status Assignment Rule: ${rule.name}
ID: ${rule.id}
Status: ${rule.status_id}
Assignee (member): ${rule.assignee_member_id}
Priority: ${rule.priority}
Enabled: ${rule.enabled}
Filter: ${formatFilter(rule.filter)}
Created: ${rule.created_at}`;
}

/**
 * Handles status-assignment-rule tool calls.
 */
export async function handleStatusRuleTool(
	toolName: string,
	args: any,
	client: PacaAPIStatusRuleClient,
): Promise<any> {
	switch (toolName) {
		case "list_status_assignment_rules": {
			const { projectId } = ListStatusRulesSchema.parse(args);
			const rules = await client.listStatusRules(projectId);
			const formatted = formatList(rules, formatStatusRule);
			return {
				content: [
					{
						type: "text",
						text: rules.length
							? `Status Assignment Rules:\n\n${formatted}`
							: "No status assignment rules configured for this project yet.",
					},
				],
			};
		}

		case "create_status_assignment_rule": {
			const { projectId, name, statusId, assigneeMemberId, filter, enabled } =
				CreateStatusRuleSchema.parse(args);
			const rule = await client.createStatusRule(projectId, {
				name,
				status_id: statusId,
				assignee_member_id: assigneeMemberId,
				filter,
				enabled,
			});
			return {
				content: [
					{
						type: "text",
						text: `Status assignment rule created successfully:\n\n${formatStatusRule(rule)}`,
					},
				],
			};
		}

		case "update_status_assignment_rule": {
			const { projectId, ruleId, name, assigneeMemberId, filter, enabled } =
				UpdateStatusRuleSchema.parse(args);
			const rule = await client.updateStatusRule(projectId, ruleId, {
				name,
				assignee_member_id: assigneeMemberId,
				filter,
				enabled,
			});
			return {
				content: [
					{
						type: "text",
						text: `Status assignment rule updated successfully:\n\n${formatStatusRule(rule)}`,
					},
				],
			};
		}

		case "delete_status_assignment_rule": {
			const { projectId, ruleId } = DeleteStatusRuleSchema.parse(args);
			await client.deleteStatusRule(projectId, ruleId);
			return {
				content: [
					{
						type: "text",
						text: `Status assignment rule ${ruleId} deleted successfully`,
					},
				],
			};
		}

		case "reorder_status_assignment_rules": {
			const { projectId, statusId, ruleIds } =
				ReorderStatusRulesSchema.parse(args);
			await client.reorderStatusRules(projectId, statusId, ruleIds);
			return {
				content: [
					{
						type: "text",
						text: `Reordered ${ruleIds.length} rule(s) for status ${statusId}`,
					},
				],
			};
		}

		default:
			throw new Error(`Unknown status assignment rule tool: ${toolName}`);
	}
}
