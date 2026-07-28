import { describe, expect, it, vi } from "vitest";

vi.mock("../../utils/index.js", () => ({
	formatList: vi.fn((items: any[], fn: any) => items.map(fn).join("---")),
}));

import {
	getStatusRuleTools,
	handleStatusRuleTool,
} from "../../tools/status-rule-tools.js";

const rule = {
	id: "r1",
	project_id: "p1",
	name: "Assign bugs to QA",
	status_id: "s-ready",
	assignee_member_id: "m1",
	filter: {},
	priority: 0,
	enabled: true,
	created_at: "2024-01-01T00:00:00Z",
	updated_at: "2024-01-01T00:00:00Z",
};

function makeClient(overrides: Record<string, any> = {}) {
	return {
		listStatusRules: vi.fn().mockResolvedValue([rule]),
		createStatusRule: vi.fn().mockResolvedValue(rule),
		updateStatusRule: vi
			.fn()
			.mockResolvedValue({ ...rule, name: "Renamed rule" }),
		deleteStatusRule: vi.fn().mockResolvedValue(undefined),
		reorderStatusRules: vi.fn().mockResolvedValue(undefined),
		...overrides,
	} as any;
}

// ---------------------------------------------------------------------------
// Tool definitions
// ---------------------------------------------------------------------------

describe("getStatusRuleTools", () => {
	it("returns 5 status assignment rule tools", () => {
		expect(getStatusRuleTools()).toHaveLength(5);
	});

	it("every tool requires projectId", () => {
		for (const tool of getStatusRuleTools()) {
			expect(tool.inputSchema.required).toContain("projectId");
		}
	});
});

// ---------------------------------------------------------------------------
// list_status_assignment_rules
// ---------------------------------------------------------------------------

describe("handleStatusRuleTool - list_status_assignment_rules", () => {
	it("calls client.listStatusRules with projectId", async () => {
		const client = makeClient();
		await handleStatusRuleTool(
			"list_status_assignment_rules",
			{ projectId: "p1" },
			client,
		);
		expect(client.listStatusRules).toHaveBeenCalledWith("p1");
	});

	it("reports when there are no rules yet", async () => {
		const client = makeClient({ listStatusRules: vi.fn().mockResolvedValue([]) });
		const result = await handleStatusRuleTool(
			"list_status_assignment_rules",
			{ projectId: "p1" },
			client,
		);
		expect(result.content[0].text).toContain("No status assignment rules");
	});

	it("includes the rule name in the response", async () => {
		const client = makeClient();
		const result = await handleStatusRuleTool(
			"list_status_assignment_rules",
			{ projectId: "p1" },
			client,
		);
		expect(result.content[0].text).toContain(rule.name);
	});
});

// ---------------------------------------------------------------------------
// create_status_assignment_rule
// ---------------------------------------------------------------------------

describe("handleStatusRuleTool - create_status_assignment_rule", () => {
	it("passes through name/statusId/assigneeMemberId and an empty filter by default", async () => {
		const client = makeClient();
		await handleStatusRuleTool(
			"create_status_assignment_rule",
			{
				projectId: "p1",
				name: "Assign bugs to QA",
				statusId: "s-ready",
				assigneeMemberId: "m1",
			},
			client,
		);
		expect(client.createStatusRule).toHaveBeenCalledWith("p1", {
			name: "Assign bugs to QA",
			status_id: "s-ready",
			assignee_member_id: "m1",
			filter: undefined,
			enabled: undefined,
		});
	});

	it("passes through a filter when provided", async () => {
		const client = makeClient();
		await handleStatusRuleTool(
			"create_status_assignment_rule",
			{
				projectId: "p1",
				name: "Assign bugs to QA",
				statusId: "s-ready",
				assigneeMemberId: "m1",
				filter: { task_type_ids: ["tt1"] },
				enabled: false,
			},
			client,
		);
		expect(client.createStatusRule).toHaveBeenCalledWith("p1", {
			name: "Assign bugs to QA",
			status_id: "s-ready",
			assignee_member_id: "m1",
			filter: { task_type_ids: ["tt1"] },
			enabled: false,
		});
	});
});

// ---------------------------------------------------------------------------
// update_status_assignment_rule
// ---------------------------------------------------------------------------

describe("handleStatusRuleTool - update_status_assignment_rule", () => {
	it("calls client.updateStatusRule with the given fields", async () => {
		const client = makeClient();
		await handleStatusRuleTool(
			"update_status_assignment_rule",
			{ projectId: "p1", ruleId: "r1", name: "Renamed rule" },
			client,
		);
		expect(client.updateStatusRule).toHaveBeenCalledWith("p1", "r1", {
			name: "Renamed rule",
			assignee_member_id: undefined,
			filter: undefined,
			enabled: undefined,
		});
	});
});

// ---------------------------------------------------------------------------
// delete_status_assignment_rule
// ---------------------------------------------------------------------------

describe("handleStatusRuleTool - delete_status_assignment_rule", () => {
	it("calls client.deleteStatusRule and confirms deletion", async () => {
		const client = makeClient();
		const result = await handleStatusRuleTool(
			"delete_status_assignment_rule",
			{ projectId: "p1", ruleId: "r1" },
			client,
		);
		expect(client.deleteStatusRule).toHaveBeenCalledWith("p1", "r1");
		expect(result.content[0].text).toContain("deleted successfully");
	});
});

// ---------------------------------------------------------------------------
// reorder_status_assignment_rules
// ---------------------------------------------------------------------------

describe("handleStatusRuleTool - reorder_status_assignment_rules", () => {
	it("calls client.reorderStatusRules with statusId and ruleIds in order", async () => {
		const client = makeClient();
		await handleStatusRuleTool(
			"reorder_status_assignment_rules",
			{ projectId: "p1", statusId: "s-ready", ruleIds: ["r2", "r1"] },
			client,
		);
		expect(client.reorderStatusRules).toHaveBeenCalledWith("p1", "s-ready", [
			"r2",
			"r1",
		]);
	});
});

// ---------------------------------------------------------------------------
// Unknown tool
// ---------------------------------------------------------------------------

describe("handleStatusRuleTool - unknown tool", () => {
	it("throws for an unrecognized tool name", async () => {
		const client = makeClient();
		await expect(
			handleStatusRuleTool("not_a_real_tool", {}, client),
		).rejects.toThrow("Unknown status assignment rule tool");
	});
});
