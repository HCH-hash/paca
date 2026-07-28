import {
	useMutation,
	useQueries,
	useQuery,
	useQueryClient,
} from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import {
	Archive as ArchiveIcon,
	ArrowLeft,
	ChevronLeft,
	ChevronRight,
	Loader2,
	Pause,
	Pencil,
	Play,
	Plus,
	Save,
	X,
} from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { AddWorkflowNodeModal } from "@/components/projects/automation/add-workflow-node-modal";
import { WorkflowCanvas } from "@/components/projects/automation/workflow-canvas";
import { WorkflowStatusTransitionsPanel } from "@/components/projects/automation/workflow-status-transitions-panel";
import { TaskDetailModal } from "@/components/projects/interactions/task-detail-modal";
import type { TaskFieldUpdate } from "@/components/projects/interactions/view-utils";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useProjectPermissions } from "@/hooks/use-project-permissions";
import { type Task, taskQueryOptions, updateTask } from "@/lib/interaction-api";
import {
	projectMembersQueryOptions,
	projectQueryOptions,
	taskStatusesQueryOptions,
	taskTypesQueryOptions,
} from "@/lib/project-api";
import { cn } from "@/lib/utils";
import {
	activateWorkflow,
	addWorkflowEdge,
	addWorkflowNode,
	archiveWorkflow,
	removeWorkflowEdge,
	removeWorkflowNode,
	revertWorkflowToDraft,
	updateWorkflow,
	updateWorkflowNode,
	workflowQueryOptions,
} from "@/lib/workflow-api";

function extractErrorMessage(err: unknown, fallback: string): string {
	const e = err as { response?: { data?: { error?: string } } };
	return e?.response?.data?.error || fallback;
}

export const Route = createFileRoute(
	"/_authenticated/projects/$projectId/automation/$workflowId",
)({
	loader: async ({
		context: { queryClient },
		params: { projectId, workflowId },
	}) => {
		await Promise.all([
			queryClient.ensureQueryData(workflowQueryOptions(projectId, workflowId)),
			queryClient.ensureQueryData(taskStatusesQueryOptions(projectId)),
			queryClient.ensureQueryData(taskTypesQueryOptions(projectId)),
			queryClient.ensureQueryData(projectMembersQueryOptions(projectId)),
			queryClient.ensureQueryData(projectQueryOptions(projectId)),
		]);
	},
	component: WorkflowBuilderPage,
});

function WorkflowBuilderPage() {
	const { t } = useTranslation("projects");
	const { projectId, workflowId } = Route.useParams();
	const qc = useQueryClient();
	const { hasProjectPermission } = useProjectPermissions(projectId);
	const canManage = hasProjectPermission("workflows.write");
	// Independent of canManage/isDraft below: tasks are normally updated while
	// a workflow is active, not just while its graph is still a draft.
	const canEditTask = hasProjectPermission("tasks.write");

	const { data: graph } = useQuery(workflowQueryOptions(projectId, workflowId));
	const { data: statuses = [] } = useQuery(taskStatusesQueryOptions(projectId));
	const { data: taskTypes = [] } = useQuery(taskTypesQueryOptions(projectId));
	const { data: members = [] } = useQuery(
		projectMembersQueryOptions(projectId),
	);
	const { data: project } = useQuery(projectQueryOptions(projectId));

	const [addNodeOpen, setAddNodeOpen] = useState(false);
	const [sidebarOpen, setSidebarOpen] = useState(true);
	const [errorMessage, setErrorMessage] = useState<string | null>(null);
	const [renaming, setRenaming] = useState(false);
	const [nameDraft, setNameDraft] = useState("");
	const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);

	const graphKey = workflowQueryOptions(projectId, workflowId).queryKey;
	const invalidate = () => qc.invalidateQueries({ queryKey: graphKey });

	const taskQueries = useQueries({
		queries: (graph?.nodes ?? []).map((n) => ({
			...taskQueryOptions(projectId, n.task_id),
		})),
	});
	const tasks = useMemo(
		() => taskQueries.map((q) => q.data).filter((t): t is Task => !!t),
		[taskQueries],
	);
	const selectedTask = useMemo<Task | null>(
		() => tasks.find((t) => t.id === selectedTaskId) ?? null,
		[tasks, selectedTaskId],
	);

	const isDraft = graph?.workflow.status === "draft";
	const isArchived = graph?.workflow.status === "archived";
	// The graph is editable in both draft and active; only archived locks it.
	const canEditGraph = canManage && !isArchived;

	function reportError(err: unknown) {
		setErrorMessage(
			extractErrorMessage(err, t("automation.builder.genericError")),
		);
	}

	const updateTaskMutation = useMutation({
		mutationFn: ({
			taskId,
			payload,
		}: {
			taskId: string;
			payload: TaskFieldUpdate;
		}) => updateTask(projectId, taskId, payload),
		onSuccess: (_, { taskId }) =>
			qc.invalidateQueries({
				queryKey: taskQueryOptions(projectId, taskId).queryKey,
			}),
	});

	const addNodeMutation = useMutation({
		mutationFn: (taskId: string) =>
			addWorkflowNode(projectId, workflowId, {
				task_id: taskId,
				pos_x: 80 + Math.random() * 200,
				pos_y: 80 + Math.random() * 200,
			}),
		onSuccess: () => {
			setAddNodeOpen(false);
			invalidate();
		},
		onError: reportError,
	});

	const moveNodeMutation = useMutation({
		mutationFn: ({
			nodeId,
			posX,
			posY,
		}: {
			nodeId: string;
			posX: number;
			posY: number;
		}) =>
			updateWorkflowNode(projectId, workflowId, nodeId, {
				pos_x: posX,
				pos_y: posY,
			}),
		onSuccess: invalidate,
	});

	const removeNodeMutation = useMutation({
		mutationFn: (nodeId: string) =>
			removeWorkflowNode(projectId, workflowId, nodeId),
		onSuccess: invalidate,
		onError: reportError,
	});

	const addEdgeMutation = useMutation({
		mutationFn: ({ source, target }: { source: string; target: string }) =>
			addWorkflowEdge(projectId, workflowId, {
				source_node_id: source,
				target_node_id: target,
			}),
		onSuccess: invalidate,
		onError: reportError,
	});

	const removeEdgeMutation = useMutation({
		mutationFn: (edgeId: string) =>
			removeWorkflowEdge(projectId, workflowId, edgeId),
		onSuccess: invalidate,
		onError: reportError,
	});

	const renameMutation = useMutation({
		mutationFn: () =>
			updateWorkflow(projectId, workflowId, { name: nameDraft }),
		onSuccess: () => {
			setRenaming(false);
			invalidate();
		},
		onError: reportError,
	});

	const activateMutation = useMutation({
		mutationFn: () => activateWorkflow(projectId, workflowId),
		onSuccess: invalidate,
		onError: reportError,
	});
	const archiveMutation = useMutation({
		mutationFn: () => archiveWorkflow(projectId, workflowId),
		onSuccess: invalidate,
		onError: reportError,
	});
	const revertMutation = useMutation({
		mutationFn: () => revertWorkflowToDraft(projectId, workflowId),
		onSuccess: invalidate,
		onError: reportError,
	});

	if (!graph) {
		return (
			<div className="flex items-center justify-center flex-1">
				<Loader2 className="size-5 animate-spin text-muted-foreground" />
			</div>
		);
	}

	const excludeTaskIds = new Set(graph.nodes.map((n) => n.task_id));

	return (
		<div className="flex flex-col min-h-0 flex-1">
			{/* Toolbar */}
			<div className="flex items-center justify-between gap-3 border-b border-border/50 px-4 py-3 shrink-0">
				<div className="flex items-center gap-3 min-w-0">
					<Link
						to="/projects/$projectId/automation"
						params={{ projectId }}
						className={buttonVariants({ variant: "ghost", size: "icon" })}
					>
						<ArrowLeft className="size-4" />
					</Link>
					{renaming ? (
						<div className="flex items-center gap-1.5">
							<Input
								autoFocus
								value={nameDraft}
								onChange={(e) => setNameDraft(e.target.value)}
								className="h-8 w-56"
							/>
							<Button
								size="icon"
								variant="ghost"
								className="size-8"
								onClick={() => renameMutation.mutate()}
							>
								<Save className="size-3.5" />
							</Button>
							<Button
								size="icon"
								variant="ghost"
								className="size-8"
								onClick={() => setRenaming(false)}
							>
								<X className="size-3.5" />
							</Button>
						</div>
					) : (
						<div className="flex items-center gap-2 min-w-0">
							<h1 className="font-semibold text-sm truncate">
								{graph.workflow.name}
							</h1>
							{canManage && !isArchived && (
								<button
									type="button"
									onClick={() => {
										setNameDraft(graph.workflow.name);
										setRenaming(true);
									}}
									className="text-muted-foreground/50 hover:text-foreground transition-colors"
								>
									<Pencil className="size-3" />
								</button>
							)}
						</div>
					)}
					<Badge
						variant={
							graph.workflow.status === "active"
								? "default"
								: graph.workflow.status === "draft"
									? "secondary"
									: "outline"
						}
					>
						{t(`automation.status.${graph.workflow.status}`)}
					</Badge>
				</div>

				<div className="flex items-center gap-2 shrink-0">
					{canEditGraph && (
						<Button
							variant="outline"
							size="sm"
							onClick={() => setAddNodeOpen(true)}
						>
							<Plus className="size-3.5 mr-1.5" />
							{t("automation.builder.addTask")}
						</Button>
					)}
					{canManage && isDraft && (
						<Button
							size="sm"
							disabled={activateMutation.isPending}
							onClick={() => activateMutation.mutate()}
						>
							{activateMutation.isPending ? (
								<Loader2 className="size-3.5 mr-1.5 animate-spin" />
							) : (
								<Play className="size-3.5 mr-1.5" />
							)}
							{t("automation.builder.activate")}
						</Button>
					)}
					{canManage && graph.workflow.status === "active" && (
						<Button
							size="sm"
							variant="outline"
							disabled={archiveMutation.isPending}
							onClick={() => archiveMutation.mutate()}
						>
							<ArchiveIcon className="size-3.5 mr-1.5" />
							{t("automation.builder.archive")}
						</Button>
					)}
					{canManage && graph.workflow.status === "active" && (
						<Button
							size="sm"
							variant="outline"
							disabled={revertMutation.isPending}
							onClick={() => revertMutation.mutate()}
						>
							<Pause className="size-3.5 mr-1.5" />
							{t("automation.builder.deactivate")}
						</Button>
					)}
				</div>
			</div>

			{isArchived && (
				<div className="px-4 py-2 bg-muted/30 border-b border-border/30 text-xs text-muted-foreground shrink-0">
					{t("automation.builder.readOnlyNoticeArchived")}
				</div>
			)}

			<div className="flex flex-1 min-h-0 relative">
				{sidebarOpen && (
					<div className="w-80 shrink-0 border-r border-border/50 overflow-y-auto p-4">
						<WorkflowStatusTransitionsPanel
							projectId={projectId}
							workflowId={workflowId}
							transitions={graph.status_transitions}
							statuses={statuses}
							canEdit={canEditGraph}
						/>
					</div>
				)}

				{/* Sidebar collapse/expand toggle — sits right on the sidebar's
				    edge so it's visually attached to what it controls. */}
				<button
					type="button"
					onClick={() => setSidebarOpen((v) => !v)}
					title={
						sidebarOpen
							? t("automation.builder.collapseSidebar")
							: t("automation.builder.expandSidebar")
					}
					className={cn(
						"absolute top-1/2 -translate-y-1/2 -translate-x-1/2 z-10 flex size-6 items-center justify-center rounded-full border border-border/60 bg-card shadow-sm text-muted-foreground/70 hover:text-foreground hover:border-border transition-colors",
						sidebarOpen ? "left-80" : "left-0",
					)}
				>
					{sidebarOpen ? (
						<ChevronLeft className="size-3.5" />
					) : (
						<ChevronRight className="size-3.5" />
					)}
				</button>

				<WorkflowCanvas
					nodes={graph.nodes}
					edges={graph.edges}
					tasks={tasks}
					statuses={statuses}
					taskTypes={taskTypes}
					members={members}
					taskIdPrefix={project?.task_id_prefix}
					canEdit={canEditGraph}
					canEditTask={canEditTask}
					onOpenTask={(taskId) => setSelectedTaskId(taskId)}
					onConnect={(source, target) =>
						addEdgeMutation.mutate({ source, target })
					}
					onMoveNode={(nodeId, posX, posY) =>
						moveNodeMutation.mutate({ nodeId, posX, posY })
					}
					onDeleteNode={(nodeId) => removeNodeMutation.mutate(nodeId)}
					onDeleteEdge={(edgeId) => removeEdgeMutation.mutate(edgeId)}
					onUpdateTask={(taskId, payload) =>
						updateTaskMutation.mutate({ taskId, payload })
					}
					errorMessage={errorMessage}
					onDismissError={() => setErrorMessage(null)}
				/>
			</div>

			<AddWorkflowNodeModal
				open={addNodeOpen}
				onClose={() => setAddNodeOpen(false)}
				onAdd={(task) => addNodeMutation.mutate(task.id)}
				projectId={projectId}
				taskIdPrefix={project?.task_id_prefix}
				excludeTaskIds={excludeTaskIds}
			/>

			<TaskDetailModal
				task={selectedTask}
				open={!!selectedTask}
				onOpenChange={(v) => {
					if (!v) setSelectedTaskId(null);
				}}
				projectId={projectId}
				statuses={statuses}
				taskTypes={taskTypes}
				members={members}
				taskIdPrefix={project?.task_id_prefix}
				canEdit={canEditTask}
			/>
		</div>
	);
}
