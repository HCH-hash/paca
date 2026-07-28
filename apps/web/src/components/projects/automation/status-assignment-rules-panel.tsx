import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Edit2, Loader2, Plus, Trash2, Zap } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
	projectMembersQueryOptions,
	taskStatusesQueryOptions,
} from "@/lib/project-api";
import {
	deleteStatusRule,
	filterSummaryCount,
	type StatusAssignmentRule,
	statusRulesQueryOptions,
	updateStatusRule,
} from "@/lib/status-rule-api";
import { StatusAssignmentRuleFormDialog } from "./status-assignment-rule-form-dialog";

export function StatusAssignmentRulesPanel({
	projectId,
	canWrite,
}: {
	projectId: string;
	canWrite: boolean;
}) {
	const { t } = useTranslation("projects");
	const queryClient = useQueryClient();
	const { data: rules, isLoading } = useQuery(
		statusRulesQueryOptions(projectId),
	);
	const { data: statuses = [] } = useQuery(taskStatusesQueryOptions(projectId));
	const { data: members = [] } = useQuery(
		projectMembersQueryOptions(projectId),
	);

	const [createOpen, setCreateOpen] = useState(false);
	const [editRule, setEditRule] = useState<StatusAssignmentRule | null>(null);
	const [deleteRule, setDeleteRuleState] =
		useState<StatusAssignmentRule | null>(null);

	const invalidate = () =>
		queryClient.invalidateQueries({
			queryKey: statusRulesQueryOptions(projectId).queryKey,
		});

	const toggleMutation = useMutation({
		mutationFn: (input: { ruleId: string; enabled: boolean }) =>
			updateStatusRule(projectId, input.ruleId, { enabled: input.enabled }),
		onSuccess: invalidate,
	});

	const deleteMutation = useMutation({
		mutationFn: (ruleId: string) => deleteStatusRule(projectId, ruleId),
		onSuccess: () => {
			setDeleteRuleState(null);
			invalidate();
		},
	});

	const sortedStatuses = [...statuses].sort((a, b) => a.position - b.position);
	const rulesByStatus = new Map<string, StatusAssignmentRule[]>();
	for (const rule of rules ?? []) {
		const list = rulesByStatus.get(rule.status_id) ?? [];
		list.push(rule);
		rulesByStatus.set(rule.status_id, list);
	}
	for (const list of rulesByStatus.values()) {
		list.sort((a, b) => a.priority - b.priority);
	}

	const memberName = (memberId: string) => {
		const m = members.find((m) => m.id === memberId);
		return m?.full_name || m?.username || memberId;
	};

	const totalRules = rules?.length ?? 0;

	return (
		<div className="rounded-xl border border-border/60 bg-card p-6">
			<div className="flex items-center justify-between mb-1">
				<div>
					<h3 className="font-[Syne] text-base font-semibold">
						{t("settings.statusRules.title")}
					</h3>
					<p className="text-xs text-muted-foreground mt-0.5 max-w-2xl">
						{t("settings.statusRules.description")}
					</p>
				</div>
				{canWrite ? (
					<Button
						size="sm"
						variant="outline"
						className="gap-1.5 border-border/60 shrink-0"
						onClick={() => setCreateOpen(true)}
					>
						<Plus className="size-3.5" />
						{t("settings.statusRules.newRule")}
					</Button>
				) : null}
			</div>

			{isLoading ? (
				<div className="rounded-xl border overflow-hidden mt-4">
					{["s1", "s2", "s3"].map((k) => (
						<div
							key={k}
							className="flex items-center gap-4 border-b px-5 py-4 last:border-0"
						>
							<Skeleton className="h-4 w-24" />
							<Skeleton className="h-4 w-32" />
							<Skeleton className="h-5 w-16 rounded-full ml-auto" />
						</div>
					))}
				</div>
			) : totalRules === 0 ? (
				<div className="flex flex-col items-center gap-4 rounded-xl border border-dashed bg-muted/20 py-16 text-center mt-4">
					<div className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground/60">
						<Zap className="size-6" />
					</div>
					<div>
						<p className="text-sm font-medium">
							{t("settings.statusRules.empty.title")}
						</p>
						<p className="mt-1 text-xs text-muted-foreground max-w-sm">
							{t("settings.statusRules.empty.description")}
						</p>
					</div>
					{canWrite ? (
						<Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
							<Plus className="size-4" />
							{t("settings.statusRules.empty.createRule")}
						</Button>
					) : null}
				</div>
			) : (
				<div className="mt-4 space-y-5">
					{sortedStatuses.map((status) => {
						const statusRules = rulesByStatus.get(status.id);
						if (!statusRules?.length) return null;
						return (
							<div key={status.id}>
								<div className="flex items-center gap-2 mb-2">
									<span
										className="inline-block size-2.5 rounded-full shrink-0"
										style={{ backgroundColor: status.color ?? "#6366f1" }}
									/>
									<h4 className="text-sm font-semibold">{status.name}</h4>
									<span className="text-xs text-muted-foreground/60">
										{t("settings.statusRules.ruleCount", {
											count: statusRules.length,
										})}
									</span>
								</div>
								<div className="rounded-xl border overflow-hidden divide-y divide-border/50">
									{statusRules.map((rule) => {
										const count = filterSummaryCount(rule.filter);
										return (
											<div
												key={rule.id}
												className="group flex items-center gap-3 px-4 py-3 bg-card"
											>
												<div className="min-w-0 flex-1">
													<p className="text-sm font-medium truncate">
														{rule.name}
													</p>
													<p className="text-xs text-muted-foreground truncate">
														{t("settings.statusRules.assignsTo", {
															member: memberName(rule.assignee_member_id),
														})}
													</p>
												</div>
												<span className="shrink-0 text-xs font-medium px-2 py-0.5 rounded-full bg-muted text-muted-foreground">
													{count === 0
														? t("settings.statusRules.allTasks")
														: t("settings.statusRules.filterCount", { count })}
												</span>
												{canWrite ? (
													<Switch
														size="sm"
														checked={rule.enabled}
														onCheckedChange={(checked) =>
															toggleMutation.mutate({
																ruleId: rule.id,
																enabled: checked,
															})
														}
														aria-label={t("settings.statusRules.enabledLabel")}
													/>
												) : null}
												{canWrite ? (
													<div className="flex items-center gap-0.5 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100">
														<Button
															variant="ghost"
															size="icon-sm"
															onClick={() => setEditRule(rule)}
															title={t("settings.statusRules.editRule")}
														>
															<Edit2 className="size-3.5" />
														</Button>
														<Button
															variant="ghost"
															size="icon-sm"
															className="text-destructive hover:text-destructive hover:bg-destructive/10"
															onClick={() => setDeleteRuleState(rule)}
															title={t("settings.statusRules.deleteRule")}
														>
															<Trash2 className="size-3.5" />
														</Button>
													</div>
												) : null}
											</div>
										);
									})}
								</div>
							</div>
						);
					})}
				</div>
			)}

			<StatusAssignmentRuleFormDialog
				projectId={projectId}
				open={createOpen}
				onOpenChange={setCreateOpen}
			/>
			{editRule ? (
				<StatusAssignmentRuleFormDialog
					projectId={projectId}
					rule={editRule}
					open={!!editRule}
					onOpenChange={(o) => {
						if (!o) setEditRule(null);
					}}
				/>
			) : null}
			{deleteRule ? (
				<Dialog
					open={!!deleteRule}
					onOpenChange={(o) => {
						if (!o) setDeleteRuleState(null);
					}}
				>
					<DialogContent className="sm:max-w-sm">
						<DialogHeader>
							<div className="flex size-10 items-center justify-center rounded-full bg-destructive/10 mb-2">
								<Trash2 className="size-5 text-destructive" />
							</div>
							<DialogTitle>
								{t("settings.statusRules.deleteDialog.title")}
							</DialogTitle>
							<DialogDescription>
								{t("settings.statusRules.deleteDialog.description", {
									name: deleteRule.name,
								})}
							</DialogDescription>
						</DialogHeader>
						<DialogFooter>
							<DialogClose
								render={
									<Button
										variant="outline"
										size="sm"
										disabled={deleteMutation.isPending}
									/>
								}
							>
								{t("settings.statusRules.deleteDialog.cancel")}
							</DialogClose>
							<Button
								variant="destructive"
								size="sm"
								disabled={deleteMutation.isPending}
								onClick={() => deleteMutation.mutate(deleteRule.id)}
							>
								{deleteMutation.isPending ? (
									<Loader2 className="size-3.5 animate-spin" />
								) : (
									<Trash2 className="size-3.5" />
								)}
								{t("settings.statusRules.deleteDialog.confirm")}
							</Button>
						</DialogFooter>
					</DialogContent>
				</Dialog>
			) : null}
		</div>
	);
}
