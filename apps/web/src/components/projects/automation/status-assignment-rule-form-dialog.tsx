import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { PRIORITY_LEVELS } from "@/components/projects/interactions/priority";
import { Button } from "@/components/ui/button";
import {
	Collapsible,
	CollapsibleContent,
	CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ApiErrorCode, getApiErrorCode } from "@/lib/api-error";
import { sprintsQueryOptions } from "@/lib/interaction-api";
import {
	type CustomFieldDefinition,
	customFieldsQueryOptions,
	type ProjectMember,
	projectMembersQueryOptions,
	taskStatusesQueryOptions,
	taskTypesQueryOptions,
} from "@/lib/project-api";
import {
	type CustomFieldFilterSpec,
	createStatusRule,
	type StatusAssignmentRule,
	statusRulesQueryOptions,
	type TaskFilterSpec,
	updateStatusRule,
} from "@/lib/status-rule-api";

interface StatusAssignmentRuleFormDialogProps {
	projectId: string;
	rule?: StatusAssignmentRule;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}

const EMPTY_FILTER: TaskFilterSpec = {};

export function StatusAssignmentRuleFormDialog({
	projectId,
	rule,
	open,
	onOpenChange,
}: StatusAssignmentRuleFormDialogProps) {
	const { t } = useTranslation("projects");
	const queryClient = useQueryClient();
	const isEdit = !!rule;

	const { data: statuses = [] } = useQuery(taskStatusesQueryOptions(projectId));
	const { data: members = [] } = useQuery(
		projectMembersQueryOptions(projectId),
	);
	const { data: taskTypes = [] } = useQuery(taskTypesQueryOptions(projectId));
	const { data: sprints = [] } = useQuery(sprintsQueryOptions(projectId));
	const { data: customFields = [] } = useQuery(
		customFieldsQueryOptions(projectId),
	);

	const [name, setName] = useState(rule?.name ?? "");
	const [statusId, setStatusId] = useState(rule?.status_id ?? "");
	const [assigneeMemberId, setAssigneeMemberId] = useState(
		rule?.assignee_member_id ?? "",
	);
	const [enabled, setEnabled] = useState(rule?.enabled ?? true);
	const [filter, setFilter] = useState<TaskFilterSpec>(
		rule?.filter ?? EMPTY_FILTER,
	);
	const [error, setError] = useState<string | null>(null);
	const [nameError, setNameError] = useState<string | null>(null);

	useEffect(() => {
		if (!open) return;
		setName(rule?.name ?? "");
		setStatusId(rule?.status_id ?? "");
		setAssigneeMemberId(rule?.assignee_member_id ?? "");
		setEnabled(rule?.enabled ?? true);
		setFilter(rule?.filter ?? EMPTY_FILTER);
		setError(null);
		setNameError(null);
	}, [open, rule]);

	const invalidate = () =>
		queryClient.invalidateQueries({
			queryKey: statusRulesQueryOptions(projectId).queryKey,
		});

	const mutation = useMutation({
		mutationFn: () => {
			if (isEdit && rule) {
				return updateStatusRule(projectId, rule.id, {
					name: name.trim(),
					assignee_member_id: assigneeMemberId,
					filter,
					enabled,
				});
			}
			return createStatusRule(projectId, {
				name: name.trim(),
				status_id: statusId,
				assignee_member_id: assigneeMemberId,
				filter,
				enabled,
			});
		},
		onSuccess: () => {
			invalidate();
			onOpenChange(false);
		},
		onError: (err: unknown) => {
			const code = getApiErrorCode(err);
			if (
				code === ApiErrorCode.StatusRuleNameInvalid ||
				code === ApiErrorCode.BadRequest
			) {
				setNameError(t("settings.statusRules.formDialog.errors.nameInvalid"));
				return;
			}
			if (code === ApiErrorCode.StatusRuleCrossProject) {
				setError(t("settings.statusRules.formDialog.errors.crossProject"));
				return;
			}
			if (code === ApiErrorCode.StatusRuleFilterUnknownCustomField) {
				setError(
					t("settings.statusRules.formDialog.errors.unknownCustomField"),
				);
				return;
			}
			setError(t("settings.statusRules.formDialog.errors.saveFailed"));
		},
	});

	const canSubmit =
		name.trim().length > 0 &&
		!!statusId &&
		!!assigneeMemberId &&
		!mutation.isPending;

	return (
		<Dialog
			open={open}
			onOpenChange={(o) => {
				onOpenChange(o);
			}}
		>
			<DialogContent className="sm:max-w-lg max-h-[85vh] overflow-y-auto">
				<DialogHeader>
					<DialogTitle>
						{isEdit
							? t("settings.statusRules.formDialog.editTitle")
							: t("settings.statusRules.formDialog.createTitle")}
					</DialogTitle>
					<DialogDescription>
						{t("settings.statusRules.formDialog.description")}
					</DialogDescription>
				</DialogHeader>

				<div className="space-y-4 py-1">
					<div className="space-y-1.5">
						<Label htmlFor="rule-name">
							{t("settings.statusRules.formDialog.nameLabel")}
						</Label>
						<Input
							id="rule-name"
							value={name}
							onChange={(e) => {
								setName(e.target.value);
								setNameError(null);
							}}
							placeholder={t("settings.statusRules.formDialog.namePlaceholder")}
							autoFocus
							className={
								nameError
									? "border-destructive focus-visible:ring-destructive/30"
									: ""
							}
						/>
						{nameError ? (
							<p className="text-xs text-destructive">{nameError}</p>
						) : null}
					</div>

					<div className="grid grid-cols-2 gap-3">
						<div className="space-y-1.5">
							<Label>{t("settings.statusRules.formDialog.statusLabel")}</Label>
							<Select
								value={statusId}
								onValueChange={(v) => setStatusId(v ?? "")}
								disabled={isEdit}
								items={statuses.map((s) => ({ value: s.id, label: s.name }))}
							>
								<SelectTrigger className="w-full">
									<SelectValue
										placeholder={t(
											"settings.statusRules.formDialog.statusPlaceholder",
										)}
									/>
								</SelectTrigger>
								<SelectContent>
									{statuses.map((s) => (
										<SelectItem key={s.id} value={s.id}>
											{s.name}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
							{isEdit ? (
								<p className="text-xs text-muted-foreground/60">
									{t("settings.statusRules.formDialog.statusImmutableHint")}
								</p>
							) : null}
						</div>
						<div className="space-y-1.5">
							<Label>
								{t("settings.statusRules.formDialog.assigneeLabel")}
							</Label>
							<Select
								value={assigneeMemberId}
								onValueChange={(v) => setAssigneeMemberId(v ?? "")}
								items={members.map((m) => ({
									value: m.id,
									label: memberLabel(m),
								}))}
							>
								<SelectTrigger className="w-full">
									<SelectValue
										placeholder={t(
											"settings.statusRules.formDialog.assigneePlaceholder",
										)}
									/>
								</SelectTrigger>
								<SelectContent>
									{members.map((m) => (
										<SelectItem key={m.id} value={m.id}>
											{memberLabel(m)}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
					</div>

					<div className="flex items-center justify-between rounded-lg border border-border/40 px-3 py-2">
						<div>
							<Label htmlFor="rule-enabled">
								{t("settings.statusRules.formDialog.enabledLabel")}
							</Label>
							<p className="text-xs text-muted-foreground/70">
								{t("settings.statusRules.formDialog.enabledHint")}
							</p>
						</div>
						<Switch
							id="rule-enabled"
							checked={enabled}
							onCheckedChange={setEnabled}
						/>
					</div>

					<div className="space-y-1">
						<p className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground/70">
							{t("settings.statusRules.formDialog.filterTitle")}
						</p>
						<p className="text-xs text-muted-foreground/70">
							{t("settings.statusRules.formDialog.filterHint")}
						</p>
					</div>

					<FilterBuilder
						filter={filter}
						onChange={setFilter}
						taskTypes={taskTypes}
						sprints={sprints}
						members={members}
						customFields={customFields}
					/>
				</div>

				{error ? (
					<p className="text-xs text-destructive bg-destructive/10 rounded-lg px-3 py-2">
						{error}
					</p>
				) : null}

				<DialogFooter>
					<DialogClose
						render={
							<Button
								variant="outline"
								size="sm"
								disabled={mutation.isPending}
							/>
						}
					>
						{t("settings.statusRules.formDialog.cancel")}
					</DialogClose>
					<Button
						size="sm"
						disabled={!canSubmit}
						onClick={() => mutation.mutate()}
					>
						{mutation.isPending ? (
							<Loader2 className="size-3.5 animate-spin" />
						) : null}
						{isEdit
							? t("settings.statusRules.formDialog.saveChanges")
							: t("settings.statusRules.formDialog.createRule")}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

function memberLabel(m: ProjectMember): string {
	return m.full_name || m.username;
}

// ── Filter builder ────────────────────────────────────────────────────────────

function FilterSection({
	title,
	summary,
	children,
}: {
	title: string;
	summary?: string;
	children: React.ReactNode;
}) {
	const [open, setOpen] = useState(false);
	return (
		<Collapsible open={open} onOpenChange={setOpen}>
			<CollapsibleTrigger className="flex w-full items-center justify-between rounded-lg border border-border/40 px-3 py-2 text-left hover:bg-muted/30 transition-colors">
				<span className="text-sm font-medium">{title}</span>
				<span className="text-xs text-muted-foreground/70">
					{summary || "—"}
				</span>
			</CollapsibleTrigger>
			<CollapsibleContent className="px-3 py-2 border border-t-0 border-border/40 rounded-b-lg -mt-px space-y-2">
				{children}
			</CollapsibleContent>
		</Collapsible>
	);
}

function CheckRow({
	label,
	checked,
	onChange,
}: {
	label: string;
	checked: boolean;
	onChange: () => void;
}) {
	return (
		<label className="flex items-center gap-2 rounded-md px-1.5 py-1 text-sm hover:bg-muted/40 cursor-pointer transition-colors">
			<input
				type="checkbox"
				checked={checked}
				onChange={onChange}
				className="size-3.5 rounded border-border/60 accent-primary"
			/>
			{label}
		</label>
	);
}

function FilterBuilder({
	filter,
	onChange,
	taskTypes,
	sprints,
	members,
	customFields,
}: {
	filter: TaskFilterSpec;
	onChange: (filter: TaskFilterSpec) => void;
	taskTypes: { id: string; name: string }[];
	sprints: { id: string; name: string }[];
	members: ProjectMember[];
	customFields: CustomFieldDefinition[];
}) {
	const { t } = useTranslation("projects");

	const toggleInList = (list: string[] | undefined, id: string) => {
		const current = list ?? [];
		return current.includes(id)
			? current.filter((x) => x !== id)
			: [...current, id];
	};

	const taskTypeIds = filter.task_type_ids ?? [];
	const sprintIds = filter.sprint_ids ?? [];
	const assigneeIds = filter.assignee_ids ?? [];
	const importanceBuckets = importanceRangesToBuckets(filter.importance_ranges);

	return (
		<div className="space-y-2">
			<FilterSection
				title={t("settings.statusRules.formDialog.filters.taskType")}
				summary={
					taskTypeIds.length
						? t("settings.statusRules.formDialog.filters.selectedCount", {
								count: taskTypeIds.length,
							})
						: t("settings.statusRules.formDialog.filters.any")
				}
			>
				{taskTypes.length === 0 ? (
					<p className="text-xs text-muted-foreground/50 px-1.5 py-1">
						{t("settings.statusRules.formDialog.filters.noTaskTypes")}
					</p>
				) : (
					taskTypes.map((tt) => (
						<CheckRow
							key={tt.id}
							label={tt.name}
							checked={taskTypeIds.includes(tt.id)}
							onChange={() =>
								onChange({
									...filter,
									task_type_ids: toggleInList(taskTypeIds, tt.id),
								})
							}
						/>
					))
				)}
			</FilterSection>

			<FilterSection
				title={t("settings.statusRules.formDialog.filters.sprint")}
				summary={
					filter.backlog_only
						? t("settings.statusRules.formDialog.filters.backlogOnly")
						: sprintIds.length
							? t("settings.statusRules.formDialog.filters.selectedCount", {
									count: sprintIds.length,
								})
							: t("settings.statusRules.formDialog.filters.any")
				}
			>
				<CheckRow
					label={t("settings.statusRules.formDialog.filters.backlogOnly")}
					checked={!!filter.backlog_only}
					onChange={() =>
						onChange({
							...filter,
							backlog_only: !filter.backlog_only,
							sprint_ids: [],
						})
					}
				/>
				{!filter.backlog_only &&
					(sprints.length === 0 ? (
						<p className="text-xs text-muted-foreground/50 px-1.5 py-1">
							{t("settings.statusRules.formDialog.filters.noSprints")}
						</p>
					) : (
						sprints.map((sp) => (
							<CheckRow
								key={sp.id}
								label={sp.name}
								checked={sprintIds.includes(sp.id)}
								onChange={() =>
									onChange({
										...filter,
										sprint_ids: toggleInList(sprintIds, sp.id),
									})
								}
							/>
						))
					))}
			</FilterSection>

			<FilterSection
				title={t("settings.statusRules.formDialog.filters.assignee")}
				summary={
					filter.assignee_null
						? t("settings.statusRules.formDialog.filters.unassigned")
						: assigneeIds.length
							? t("settings.statusRules.formDialog.filters.selectedCount", {
									count: assigneeIds.length,
								})
							: t("settings.statusRules.formDialog.filters.any")
				}
			>
				<CheckRow
					label={t("settings.statusRules.formDialog.filters.unassigned")}
					checked={!!filter.assignee_null}
					onChange={() =>
						onChange({ ...filter, assignee_null: !filter.assignee_null })
					}
				/>
				{members.map((m) => (
					<CheckRow
						key={m.id}
						label={memberLabel(m)}
						checked={assigneeIds.includes(m.id)}
						onChange={() =>
							onChange({
								...filter,
								assignee_ids: toggleInList(assigneeIds, m.id),
							})
						}
					/>
				))}
			</FilterSection>

			<FilterSection
				title={t("settings.statusRules.formDialog.filters.tags")}
				summary={
					filter.tags?.length
						? t("settings.statusRules.formDialog.filters.selectedCount", {
								count: filter.tags.length,
							})
						: t("settings.statusRules.formDialog.filters.any")
				}
			>
				<TagsInput
					tags={filter.tags ?? []}
					onChange={(tags) => onChange({ ...filter, tags })}
				/>
			</FilterSection>

			<FilterSection
				title={t("settings.statusRules.formDialog.filters.importance")}
				summary={
					importanceBuckets.length
						? t("settings.statusRules.formDialog.filters.selectedCount", {
								count: importanceBuckets.length,
							})
						: t("settings.statusRules.formDialog.filters.any")
				}
			>
				{PRIORITY_LEVELS.map((level) => (
					<CheckRow
						key={level.value}
						label={t(level.labelKey)}
						checked={importanceBuckets.includes(level.value)}
						onChange={() => {
							const next = importanceBuckets.includes(level.value)
								? importanceBuckets.filter((b) => b !== level.value)
								: [...importanceBuckets, level.value];
							onChange({
								...filter,
								importance_ranges: bucketsToImportanceRanges(next),
							});
						}}
					/>
				))}
			</FilterSection>

			<FilterSection
				title={t("settings.statusRules.formDialog.filters.storyPoints")}
				summary={
					filter.story_points_min != null || filter.story_points_max != null
						? `${filter.story_points_min ?? ""}–${filter.story_points_max ?? ""}`
						: t("settings.statusRules.formDialog.filters.any")
				}
			>
				<div className="grid grid-cols-2 gap-2">
					<Input
						type="number"
						placeholder={t("settings.statusRules.formDialog.filters.min")}
						value={filter.story_points_min ?? ""}
						onChange={(e) =>
							onChange({
								...filter,
								story_points_min:
									e.target.value === "" ? undefined : Number(e.target.value),
							})
						}
					/>
					<Input
						type="number"
						placeholder={t("settings.statusRules.formDialog.filters.max")}
						value={filter.story_points_max ?? ""}
						onChange={(e) =>
							onChange({
								...filter,
								story_points_max:
									e.target.value === "" ? undefined : Number(e.target.value),
							})
						}
					/>
				</div>
			</FilterSection>

			<FilterSection
				title={t("settings.statusRules.formDialog.filters.dates")}
				summary={
					filter.start_date_after ||
					filter.start_date_before ||
					filter.due_date_after ||
					filter.due_date_before
						? t("settings.statusRules.formDialog.filters.customized")
						: t("settings.statusRules.formDialog.filters.any")
				}
			>
				<div className="space-y-2">
					<p className="text-xs font-medium text-muted-foreground/70">
						{t("settings.statusRules.formDialog.filters.startDate")}
					</p>
					<div className="grid grid-cols-2 gap-2">
						<Input
							type="date"
							value={filter.start_date_after ?? ""}
							onChange={(e) =>
								onChange({
									...filter,
									start_date_after: e.target.value || undefined,
								})
							}
						/>
						<Input
							type="date"
							value={filter.start_date_before ?? ""}
							onChange={(e) =>
								onChange({
									...filter,
									start_date_before: e.target.value || undefined,
								})
							}
						/>
					</div>
					<p className="text-xs font-medium text-muted-foreground/70">
						{t("settings.statusRules.formDialog.filters.dueDate")}
					</p>
					<div className="grid grid-cols-2 gap-2">
						<Input
							type="date"
							value={filter.due_date_after ?? ""}
							onChange={(e) =>
								onChange({
									...filter,
									due_date_after: e.target.value || undefined,
								})
							}
						/>
						<Input
							type="date"
							value={filter.due_date_before ?? ""}
							onChange={(e) =>
								onChange({
									...filter,
									due_date_before: e.target.value || undefined,
								})
							}
						/>
					</div>
				</div>
			</FilterSection>

			{customFields.map((cf) => (
				<CustomFieldFilterSection
					key={cf.id}
					field={cf}
					value={filter.custom_fields?.[cf.field_key]}
					onChange={(value) => {
						const next = { ...(filter.custom_fields ?? {}) };
						if (!value) {
							delete next[cf.field_key];
						} else {
							next[cf.field_key] = value;
						}
						onChange({ ...filter, custom_fields: next });
					}}
				/>
			))}
		</div>
	);
}

function TagsInput({
	tags,
	onChange,
}: {
	tags: string[];
	onChange: (tags: string[]) => void;
}) {
	const { t } = useTranslation("projects");
	const [input, setInput] = useState("");

	const add = () => {
		const trimmed = input.trim();
		if (!trimmed || tags.includes(trimmed)) return;
		onChange([...tags, trimmed]);
		setInput("");
	};

	return (
		<div className="space-y-2">
			<div className="flex flex-wrap gap-1.5">
				{tags.map((tag) => (
					<span
						key={tag}
						className="inline-flex items-center gap-1 rounded-md bg-muted/50 px-2 py-0.5 text-xs font-medium border border-border/20"
					>
						{tag}
						<button
							type="button"
							onClick={() => onChange(tags.filter((t) => t !== tag))}
							className="text-muted-foreground/60 hover:text-destructive"
						>
							<X className="size-2.5" />
						</button>
					</span>
				))}
			</div>
			<div className="flex gap-1.5">
				<Input
					value={input}
					onChange={(e) => setInput(e.target.value)}
					onKeyDown={(e) => {
						if (e.key === "Enter") {
							e.preventDefault();
							add();
						}
					}}
					placeholder={t("settings.statusRules.formDialog.filters.addTag")}
				/>
				<Button size="icon-sm" variant="outline" onClick={add}>
					<Plus className="size-3.5" />
				</Button>
			</div>
		</div>
	);
}

function CustomFieldFilterSection({
	field,
	value,
	onChange,
}: {
	field: CustomFieldDefinition;
	value: CustomFieldFilterSpec | undefined;
	onChange: (value: CustomFieldFilterSpec | undefined) => void;
}) {
	const { t } = useTranslation("projects");
	const isEmpty = !value;

	return (
		<FilterSection
			title={field.display_name}
			summary={
				isEmpty
					? t("settings.statusRules.formDialog.filters.any")
					: t("settings.statusRules.formDialog.filters.customized")
			}
		>
			{field.field_type === "select" || field.field_type === "multi_select" ? (
				field.options.map((opt) => (
					<CheckRow
						key={opt}
						label={opt}
						checked={!!value?.values?.includes(opt)}
						onChange={() => {
							const current = value?.values ?? [];
							const next = current.includes(opt)
								? current.filter((v) => v !== opt)
								: [...current, opt];
							onChange(next.length ? { values: next } : undefined);
						}}
					/>
				))
			) : field.field_type === "boolean" ? (
				["true", "false"].map((opt) => (
					<CheckRow
						key={opt}
						label={
							opt === "true"
								? t("settings.statusRules.formDialog.filters.yes")
								: t("settings.statusRules.formDialog.filters.no")
						}
						checked={!!value?.values?.includes(opt)}
						onChange={() => {
							const current = value?.values ?? [];
							const next = current.includes(opt)
								? current.filter((v) => v !== opt)
								: [...current, opt];
							onChange(next.length ? { values: next } : undefined);
						}}
					/>
				))
			) : field.field_type === "number" ? (
				<div className="grid grid-cols-2 gap-2">
					<Input
						type="number"
						placeholder={t("settings.statusRules.formDialog.filters.min")}
						value={value?.min ?? ""}
						onChange={(e) => {
							const min =
								e.target.value === "" ? undefined : Number(e.target.value);
							onChange(
								min == null && value?.max == null
									? undefined
									: { ...value, min },
							);
						}}
					/>
					<Input
						type="number"
						placeholder={t("settings.statusRules.formDialog.filters.max")}
						value={value?.max ?? ""}
						onChange={(e) => {
							const max =
								e.target.value === "" ? undefined : Number(e.target.value);
							onChange(
								max == null && value?.min == null
									? undefined
									: { ...value, max },
							);
						}}
					/>
				</div>
			) : field.field_type === "date" ? (
				<div className="grid grid-cols-2 gap-2">
					<Input
						type="date"
						value={value?.after ?? ""}
						onChange={(e) => {
							const after = e.target.value || undefined;
							onChange(
								after == null && value?.before == null
									? undefined
									: { ...value, after },
							);
						}}
					/>
					<Input
						type="date"
						value={value?.before ?? ""}
						onChange={(e) => {
							const before = e.target.value || undefined;
							onChange(
								before == null && value?.after == null
									? undefined
									: { ...value, before },
							);
						}}
					/>
				</div>
			) : (
				<Input
					value={value?.contains ?? ""}
					placeholder={t("settings.statusRules.formDialog.filters.contains")}
					onChange={(e) => {
						const contains = e.target.value || undefined;
						onChange(contains == null ? undefined : { contains });
					}}
				/>
			)}
		</FilterSection>
	);
}

// ── Importance bucket <-> range conversion ───────────────────────────────────
// Mirrors getImportanceBucketBounds in interactions/priority.ts.

function bucketBounds(bucket: number): { min: number; max: number } {
	switch (bucket) {
		case 0:
			return { min: 0, max: 0 };
		case 1:
			return { min: 1, max: 19 };
		case 2:
			return { min: 20, max: 49 };
		case 3:
			return { min: 50, max: 99 };
		default:
			return { min: 100, max: 2147483647 };
	}
}

function bucketsToImportanceRanges(
	buckets: number[],
): TaskFilterSpec["importance_ranges"] {
	if (buckets.length === 0) return undefined;
	return buckets.map((b) => bucketBounds(b));
}

function importanceRangesToBuckets(
	ranges: TaskFilterSpec["importance_ranges"],
): number[] {
	if (!ranges?.length) return [];
	const buckets: number[] = [];
	for (let b = 0; b <= 4; b++) {
		const { min, max } = bucketBounds(b);
		if (ranges.some((r) => r.min === min && r.max === max)) buckets.push(b);
	}
	return buckets;
}
