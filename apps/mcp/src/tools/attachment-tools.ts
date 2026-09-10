import { readFile } from "node:fs/promises";
import { basename, extname } from "node:path";
import type { Tool } from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";
import type { PacaAPIDocClient, PacaAPIViewsClient } from "../api/index.js";
import { formatFileSize, formatList, formatToolError } from "../utils/index.js";

const ListTaskAttachmentsSchema = z.object({
	projectId: z.string(),
	taskId: z.string(),
});

const UploadTaskAttachmentSchema = z.object({
	projectId: z.string(),
	taskId: z.string(),
	filePath: z.string(),
	fileName: z.string().optional(),
	contentType: z.string().optional(),
});

// Minimal extension → MIME map for inferring content_type when the caller
// doesn't pass one. Kept small and dependency-free; anything unknown falls back
// to application/octet-stream (a valid, if generic, content type).
const EXT_CONTENT_TYPES: Record<string, string> = {
	txt: "text/plain",
	md: "text/markdown",
	csv: "text/csv",
	log: "text/plain",
	json: "application/json",
	xml: "application/xml",
	yml: "application/yaml",
	yaml: "application/yaml",
	html: "text/html",
	htm: "text/html",
	css: "text/css",
	js: "text/javascript",
	pdf: "application/pdf",
	png: "image/png",
	jpg: "image/jpeg",
	jpeg: "image/jpeg",
	gif: "image/gif",
	webp: "image/webp",
	svg: "image/svg+xml",
	zip: "application/zip",
	gz: "application/gzip",
	tar: "application/x-tar",
	doc: "application/msword",
	docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	xls: "application/vnd.ms-excel",
	xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	ppt: "application/vnd.ms-powerpoint",
	pptx: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
};

function guessContentType(fileName: string): string {
	const ext = extname(fileName).slice(1).toLowerCase();
	return EXT_CONTENT_TYPES[ext] ?? "application/octet-stream";
}

const GetAttachmentDownloadURLSchema = z.object({
	projectId: z.string(),
	taskId: z.string(),
	attachmentId: z.string(),
});

const ReadTaskAttachmentSchema = z.object({
	projectId: z.string(),
	taskId: z.string(),
	attachmentId: z.string(),
});

const DeleteTaskAttachmentSchema = z.object({
	projectId: z.string(),
	taskId: z.string(),
	attachmentId: z.string(),
});

const ReadDocFileSchema = z.object({
	projectId: z.string(),
	docId: z.string(),
	fileId: z.string(),
});

// Content types that are safe to decode and hand back as plain text.
const TEXT_MIME_TYPES = new Set([
	"application/json",
	"application/xml",
	"application/yaml",
	"application/x-yaml",
	"application/javascript",
	"application/typescript",
	"application/x-sh",
	"application/sql",
	"application/x-ndjson",
	"application/toml",
]);

// Extension fallback for files uploaded with a generic content type (e.g.
// application/octet-stream), since uploaders don't always set one accurately.
const TEXT_EXTENSIONS = new Set([
	"txt",
	"md",
	"markdown",
	"csv",
	"tsv",
	"log",
	"json",
	"yml",
	"yaml",
	"xml",
	"html",
	"htm",
	"css",
	"scss",
	"less",
	"js",
	"jsx",
	"ts",
	"tsx",
	"mjs",
	"cjs",
	"py",
	"go",
	"rb",
	"java",
	"c",
	"cpp",
	"h",
	"hpp",
	"cs",
	"php",
	"sh",
	"bash",
	"zsh",
	"sql",
	"toml",
	"ini",
	"cfg",
	"conf",
	"env",
	"graphql",
	"proto",
	"rs",
	"kt",
	"swift",
	"vue",
	"svelte",
]);

// Conventionally-named text files that don't carry a recognizable extension,
// matched case-insensitively on the full file name.
const TEXT_FILENAMES = new Set([
	"dockerfile",
	"makefile",
	"rakefile",
	"gemfile",
	"gemfile.lock",
	"procfile",
	"vagrantfile",
	"license",
	"licence",
	"readme",
	"changelog",
	"contributing",
	"authors",
	"notice",
	".gitignore",
	".dockerignore",
	".gitattributes",
	".editorconfig",
	".npmrc",
	".env",
]);

// Image types the MCP "image" content block (and the LLMs consuming it) can
// actually render. Anything else falls back to the binary path below.
const IMAGE_MIME_TYPES = new Set([
	"image/png",
	"image/jpeg",
	"image/gif",
	"image/webp",
]);

const MAX_TEXT_BYTES = 2 * 1024 * 1024; // 2 MB
const MAX_IMAGE_BYTES = 5 * 1024 * 1024; // 5 MB

type AttachmentKind = "text" | "image" | "binary";

/** Lower-cases a content type and drops its parameters (e.g. "; charset=utf-8"). */
function normalizeContentType(contentType: string | undefined): string {
	return (contentType || "").toLowerCase().split(";")[0].trim();
}

function classifyAttachment(
	fileName: string,
	contentType: string | undefined,
): AttachmentKind {
	const normalizedType = normalizeContentType(contentType);
	if (IMAGE_MIME_TYPES.has(normalizedType)) return "image";
	if (normalizedType.startsWith("text/")) return "text";
	if (TEXT_MIME_TYPES.has(normalizedType)) return "text";

	if (TEXT_FILENAMES.has(fileName.toLowerCase().trim())) return "text";

	const ext = fileName.split(".").pop()?.toLowerCase() ?? "";
	if (TEXT_EXTENSIONS.has(ext)) return "text";

	return "binary";
}

/**
 * Returns all attachment-related MCP tools: task attachments, plus
 * read_doc_file for files attached to a document (the backend serves both
 * from its attachment domain, and read_doc_file shares read_task_attachment's
 * classification and size limits).
 */
export function getAttachmentTools(): Tool[] {
	return [
		{
			name: "upload_task_attachment",
			description:
				"Upload a local file as an attachment on a task. Reads the file at " +
				"filePath from the machine this MCP server runs on (e.g. a file the " +
				"agent just created in its workspace) and attaches it to the task. " +
				"The file name and content type are inferred from filePath unless " +
				"given explicitly.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					taskId: {
						type: "string",
						description:
							"The technical UUID of the task (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_tasks to get the task ID.",
					},
					filePath: {
						type: "string",
						description:
							"Absolute or working-directory-relative path to the local file to upload.",
					},
					fileName: {
						type: "string",
						description:
							"Optional name to store the attachment under. Defaults to the base name of filePath.",
					},
					contentType: {
						type: "string",
						description:
							"Optional MIME type (e.g. 'application/pdf'). Defaults to a guess from the file extension.",
					},
				},
				required: ["projectId", "taskId", "filePath"],
			},
		},
		{
			name: "list_task_attachments",
			description: "List all attachments for a task",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					taskId: {
						type: "string",
						description:
							"The technical UUID of the task (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_tasks to get the task ID.",
					},
				},
				required: ["projectId", "taskId"],
			},
		},
		{
			name: "get_attachment_download_url",
			description: "Get a download URL for an attachment",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					taskId: {
						type: "string",
						description:
							"The technical UUID of the task (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_tasks to get the task ID.",
					},
					attachmentId: {
						type: "string",
						description:
							"The technical UUID of the attachment (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_task_attachments to get the attachment ID.",
					},
				},
				required: ["projectId", "taskId", "attachmentId"],
			},
		},
		{
			name: "read_task_attachment",
			description:
				"Download and read the contents of a task attachment. Text-based files " +
				"(code, markdown, JSON, YAML, CSV, logs, etc.) are returned as plain text; " +
				"images (PNG, JPEG, GIF, WebP) are returned as a viewable image. Files over " +
				"2 MB (text) or 5 MB (images), and other binary formats (PDF, zip, docx, " +
				"etc.), can't be read this way — use get_attachment_download_url for those.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					taskId: {
						type: "string",
						description:
							"The technical UUID of the task (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_tasks to get the task ID.",
					},
					attachmentId: {
						type: "string",
						description:
							"The technical UUID of the attachment (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_task_attachments or get_task to get the attachment ID.",
					},
				},
				required: ["projectId", "taskId", "attachmentId"],
			},
		},
		{
			name: "delete_task_attachment",
			description: "Delete an attachment from a task",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					taskId: {
						type: "string",
						description:
							"The technical UUID of the task (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_tasks to get the task ID.",
					},
					attachmentId: {
						type: "string",
						description:
							"The technical UUID of the attachment (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_task_attachments to get the attachment ID.",
					},
				},
				required: ["projectId", "taskId", "attachmentId"],
			},
		},
		{
			name: "read_doc_file",
			description:
				"Download and read a file attached to a document, such as an image " +
				"pasted into it. Images (PNG, JPEG, GIF, WebP) are returned as a " +
				"viewable image; text-based files (code, markdown, JSON, YAML, CSV, " +
				"logs, etc.) are returned as plain text. Files over 2 MB (text) or " +
				"5 MB (images), and other binary formats (PDF, zip, docx, etc.), " +
				"can't be read this way. A document's content refers to its files " +
				"as docfile://{projectId}/{docId}/{fileId}.",
			inputSchema: {
				type: "object",
				properties: {
					projectId: {
						type: "string",
						description:
							"The technical UUID of the project (e.g., '550e8400-e29b-41d4-a716-446655440000'). Use list_projects to get the project ID. Do NOT use the project name.",
					},
					docId: {
						type: "string",
						description:
							"The technical UUID of the document the file is attached to (e.g., '550e8400-e29b-41d4-a716-446655440000'). read_doc shows a document's ID.",
					},
					fileId: {
						type: "string",
						description:
							"The technical UUID of the file (e.g., '550e8400-e29b-41d4-a716-446655440000') — the last segment of a docfile://{projectId}/{docId}/{fileId} reference in the document's content.",
					},
				},
				required: ["projectId", "docId", "fileId"],
			},
		},
	];
}

function formatAttachment(attachment: any): string {
	return `Attachment: ${attachment.file?.file_name || "Unknown"}
ID: ${attachment.id}
Size: ${attachment.file?.file_size || 0} bytes
Type: ${attachment.file?.content_type || "Unknown"}
Uploaded by: ${attachment.created_by || "Unknown"}
Uploaded at: ${attachment.created_at}`;
}

/**
 * Handles attachment tool calls.
 */
export async function handleAttachmentTool(
	toolName: string,
	args: any,
	viewsClient: PacaAPIViewsClient,
): Promise<any> {
	switch (toolName) {
		case "upload_task_attachment": {
			const { projectId, taskId, filePath, fileName, contentType } =
				UploadTaskAttachmentSchema.parse(args);

			let data: Buffer;
			try {
				data = await readFile(filePath);
			} catch (err) {
				return {
					content: [
						{
							type: "text",
							text: `Could not read file at "${filePath}": ${(err as Error).message}`,
						},
					],
					isError: true,
				};
			}
			if (data.byteLength === 0) {
				return {
					content: [
						{ type: "text", text: `File "${filePath}" is empty; nothing to upload.` },
					],
					isError: true,
				};
			}

			const name = fileName || basename(filePath);
			const type = contentType || guessContentType(name);
			const attachment = await viewsClient.uploadTaskAttachment(
				projectId,
				taskId,
				{ fileName: name, contentType: type, data },
			);
			return {
				content: [
					{
						type: "text",
						text: `Uploaded "${name}" (${formatFileSize(data.byteLength)}) to task ${taskId}.\n\n${formatAttachment(attachment)}`,
					},
				],
			};
		}

		case "list_task_attachments": {
			const { projectId, taskId } = ListTaskAttachmentsSchema.parse(args);
			const attachments = await viewsClient.listTaskAttachments(
				projectId,
				taskId,
			);
			const formatted = formatList(attachments, formatAttachment);
			return {
				content: [
					{
						type: "text",
						text: `Attachments:\n\n${formatted}`,
					},
				],
			};
		}

		case "get_attachment_download_url": {
			const { projectId, taskId, attachmentId } =
				GetAttachmentDownloadURLSchema.parse(args);
			const result = await viewsClient.getAttachmentDownloadURL(
				projectId,
				taskId,
				attachmentId,
			);
			return {
				content: [
					{
						type: "text",
						text: `Download URL: ${result}`,
					},
				],
			};
		}

		case "read_task_attachment": {
			const { projectId, taskId, attachmentId } =
				ReadTaskAttachmentSchema.parse(args);

			const attachments = await viewsClient.listTaskAttachments(
				projectId,
				taskId,
			);
			const attachment = attachments.find((a: any) => a.id === attachmentId);
			if (!attachment) {
				return {
					content: [
						{
							type: "text",
							text: `Attachment ${attachmentId} not found on task ${taskId}.`,
						},
					],
					isError: true,
				};
			}

			const { file } = attachment;
			const kind = classifyAttachment(file.file_name, file.content_type);

			if (kind === "binary") {
				return {
					content: [
						{
							type: "text",
							text: `"${file.file_name}" (${file.content_type || "unknown type"}, ${formatFileSize(file.file_size)}) can't be read as text or an image. Use get_attachment_download_url to download it directly.`,
						},
					],
					isError: true,
				};
			}

			const maxBytes = kind === "image" ? MAX_IMAGE_BYTES : MAX_TEXT_BYTES;
			if (file.file_size > maxBytes) {
				return {
					content: [
						{
							type: "text",
							text: `"${file.file_name}" is ${formatFileSize(file.file_size)}, which exceeds the ${formatFileSize(maxBytes)} limit for reading ${kind === "image" ? "images" : "text files"} inline. Use get_attachment_download_url to download it directly.`,
						},
					],
					isError: true,
				};
			}

			const { buffer, contentType } =
				await viewsClient.downloadAttachmentContent(
					projectId,
					taskId,
					attachmentId,
				);
			const effectiveType =
				contentType?.split(";")[0]?.trim() ||
				file.content_type ||
				"application/octet-stream";

			if (kind === "image") {
				return {
					content: [
						{
							type: "image",
							data: Buffer.from(buffer).toString("base64"),
							mimeType: effectiveType,
						},
					],
				};
			}

			const text = Buffer.from(buffer).toString("utf-8");
			return {
				content: [
					{
						type: "text",
						text: `File: ${file.file_name} (${formatFileSize(file.file_size)})\n\n${text}`,
					},
				],
			};
		}

		case "delete_task_attachment": {
			const { projectId, taskId, attachmentId } =
				DeleteTaskAttachmentSchema.parse(args);
			await viewsClient.deleteTaskAttachment(projectId, taskId, attachmentId);
			return {
				content: [
					{
						type: "text",
						text: `Attachment ${attachmentId} deleted successfully`,
					},
				],
			};
		}

		default:
			throw new Error(`Unknown attachment tool: ${toolName}`);
	}
}

/**
 * Handles doc file tool calls. Separate from handleAttachmentTool only
 * because doc files are served by the doc client rather than the views
 * client.
 *
 * Wrapped in its own try/catch, like handleDocActivityTool: tools/index.ts
 * returns this promise without awaiting it, so its outer try/catch never sees
 * an async rejection — every error path here must resolve to an isError
 * result instead.
 */
export async function handleDocFileTool(
	toolName: string,
	args: any,
	docClient: PacaAPIDocClient,
): Promise<any> {
	try {
		switch (toolName) {
			case "read_doc_file": {
				const { projectId, docId, fileId } = ReadDocFileSchema.parse(args);

				// Doc files have no metadata endpoint, so the download's own
				// headers are the only source of the file's type, size and name.
				// openDocFile hands them over before any of the body is read.
				const download = await docClient.openDocFile(
					projectId,
					docId,
					fileId,
				);
				const fileName = download.fileName || fileId;
				const kind = classifyAttachment(fileName, download.contentType);

				if (kind === "binary") {
					await download.discard();
					const size =
						download.contentLength === undefined
							? ""
							: `, ${formatFileSize(download.contentLength)}`;
					return {
						content: [
							{
								type: "text",
								text: `"${fileName}" (${download.contentType || "unknown type"}${size}) can't be read as text or an image. Only images (PNG, JPEG, GIF, WebP) and text files can be read inline.`,
							},
						],
						isError: true,
					};
				}

				const maxBytes = kind === "image" ? MAX_IMAGE_BYTES : MAX_TEXT_BYTES;
				const limit = `the ${formatFileSize(maxBytes)} limit for reading ${kind === "image" ? "images" : "text files"} inline`;

				if (
					download.contentLength !== undefined &&
					download.contentLength > maxBytes
				) {
					await download.discard();
					return {
						content: [
							{
								type: "text",
								text: `"${fileName}" is ${formatFileSize(download.contentLength)}, which exceeds ${limit}.`,
							},
						],
						isError: true,
					};
				}

				// Still bounded when Content-Length is missing or understated.
				const buffer = await download.read(maxBytes);
				if (buffer === null) {
					return {
						content: [
							{
								type: "text",
								text: `"${fileName}" exceeds ${limit}.`,
							},
						],
						isError: true,
					};
				}

				if (kind === "image") {
					return {
						content: [
							{
								type: "image",
								data: Buffer.from(buffer).toString("base64"),
								mimeType: normalizeContentType(download.contentType),
							},
						],
					};
				}

				const text = Buffer.from(buffer).toString("utf-8");
				return {
					content: [
						{
							type: "text",
							text: `File: ${fileName} (${formatFileSize(buffer.byteLength)})\n\n${text}`,
						},
					],
				};
			}

			default:
				throw new Error(`Unknown doc file tool: ${toolName}`);
		}
	} catch (error) {
		return {
			content: [{ type: "text", text: `Error: ${formatToolError(error)}` }],
			isError: true,
		};
	}
}
