import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../utils/index.js", () => ({
	formatList: vi.fn((items: any[], fn: any) => items.map(fn).join("---")),
	formatFileSize: vi.fn((bytes: number) => `${bytes} bytes`),
	formatToolError: vi.fn((error: unknown) =>
		error instanceof Error ? error.message : String(error),
	),
	formatApiRequestError: vi.fn(
		(status: number, statusText: string, errorText: string) =>
			`API request failed: ${status} ${statusText} - ${errorText}`,
	),
	// Imported by doc-client.ts (for doc comments); unused by these tests.
	markdownToBlocknote: vi.fn(() => []),
}));

vi.mock("node:fs/promises", () => ({
	readFile: vi.fn(),
}));

import { readFile } from "node:fs/promises";
import { PacaAPIDocClient } from "../../api/doc-client.js";
import { getToolPermission } from "../../permissions.js";
import {
	getAttachmentTools,
	handleAttachmentTool,
	handleDocFileTool,
} from "../../tools/attachment-tools.js";

const attachment = {
	id: "att-1",
	file: {
		file_name: "photo.png",
		file_size: 1024,
		content_type: "image/png",
	},
	created_by: "user-1",
	created_at: "2024-01-01T00:00:00Z",
};

function makeClient(overrides: Record<string, any> = {}) {
	return {
		listTaskAttachments: vi.fn().mockResolvedValue([attachment]),
		getAttachmentDownloadURL: vi
			.fn()
			.mockResolvedValue("https://cdn.example.com/photo.png"),
		downloadAttachmentContent: vi.fn().mockResolvedValue({
			buffer: new TextEncoder().encode("hello world").buffer,
			contentType: "image/png",
		}),
		deleteTaskAttachment: vi.fn().mockResolvedValue(undefined),
		uploadTaskAttachment: vi.fn().mockResolvedValue(attachment),
		...overrides,
	} as any;
}

// ---------------------------------------------------------------------------
// getAttachmentTools
// ---------------------------------------------------------------------------

describe("getAttachmentTools", () => {
	it("returns 6 tools", () => {
		expect(getAttachmentTools()).toHaveLength(6);
	});

	it("includes upload, list, download-url, read, delete, and doc file tools", () => {
		const names = getAttachmentTools().map((t) => t.name);
		expect(names).toContain("upload_task_attachment");
		expect(names).toContain("list_task_attachments");
		expect(names).toContain("get_attachment_download_url");
		expect(names).toContain("read_task_attachment");
		expect(names).toContain("delete_task_attachment");
		expect(names).toContain("read_doc_file");
	});

	it("requires projectId, docId, and fileId for read_doc_file", () => {
		const tool = getAttachmentTools().find((t) => t.name === "read_doc_file");
		expect(tool?.inputSchema.required).toEqual([
			"projectId",
			"docId",
			"fileId",
		]);
	});

	it("gates read_doc_file on docs.read, so it's listed exactly when docs are readable", () => {
		const perm = getToolPermission("read_doc_file");
		expect(perm?.permissionKey).toBe("docs.read");
		expect(perm?.requiresProject).toBe(true);
	});
});

// ---------------------------------------------------------------------------
// upload_task_attachment
// ---------------------------------------------------------------------------

describe("upload_task_attachment", () => {
	it("reads the file and uploads it, inferring name and content type", async () => {
		vi.mocked(readFile).mockResolvedValue(Buffer.from("PNGDATA") as any);
		const client = makeClient();

		const result = await handleAttachmentTool(
			"upload_task_attachment",
			{ projectId: "p1", taskId: "t1", filePath: "/tmp/photo.png" },
			client,
		);

		expect(readFile).toHaveBeenCalledWith("/tmp/photo.png");
		expect(client.uploadTaskAttachment).toHaveBeenCalledWith("p1", "t1", {
			fileName: "photo.png",
			contentType: "image/png",
			data: expect.any(Buffer),
		});
		expect(result.isError).toBeFalsy();
		expect(result.content[0].text).toContain("Uploaded");
	});

	it("honors explicit fileName and contentType overrides", async () => {
		vi.mocked(readFile).mockResolvedValue(Buffer.from("data") as any);
		const client = makeClient();

		await handleAttachmentTool(
			"upload_task_attachment",
			{
				projectId: "p1",
				taskId: "t1",
				filePath: "/tmp/x.bin",
				fileName: "report.pdf",
				contentType: "application/pdf",
			},
			client,
		);

		expect(client.uploadTaskAttachment).toHaveBeenCalledWith(
			"p1",
			"t1",
			expect.objectContaining({
				fileName: "report.pdf",
				contentType: "application/pdf",
			}),
		);
	});

	it("returns an error when the file cannot be read", async () => {
		vi.mocked(readFile).mockRejectedValue(new Error("ENOENT"));
		const client = makeClient();

		const result = await handleAttachmentTool(
			"upload_task_attachment",
			{ projectId: "p1", taskId: "t1", filePath: "/tmp/missing" },
			client,
		);

		expect(result.isError).toBe(true);
		expect(client.uploadTaskAttachment).not.toHaveBeenCalled();
	});

	it("rejects an empty file", async () => {
		vi.mocked(readFile).mockResolvedValue(Buffer.alloc(0) as any);
		const client = makeClient();

		const result = await handleAttachmentTool(
			"upload_task_attachment",
			{ projectId: "p1", taskId: "t1", filePath: "/tmp/empty.txt" },
			client,
		);

		expect(result.isError).toBe(true);
		expect(client.uploadTaskAttachment).not.toHaveBeenCalled();
	});
});

// ---------------------------------------------------------------------------
// list_task_attachments
// ---------------------------------------------------------------------------

describe("handleAttachmentTool – list_task_attachments", () => {
	it("calls viewsClient.listTaskAttachments with projectId and taskId", async () => {
		const client = makeClient();
		await handleAttachmentTool(
			"list_task_attachments",
			{ projectId: "p1", taskId: "t1" },
			client,
		);
		expect(client.listTaskAttachments).toHaveBeenCalledWith("p1", "t1");
	});

	it("includes 'Attachments:' header in the response", async () => {
		const result = await handleAttachmentTool(
			"list_task_attachments",
			{ projectId: "p1", taskId: "t1" },
			makeClient(),
		);
		expect(result.content[0].text).toContain("Attachments:");
	});

	it("includes attachment file name in the formatted output", async () => {
		const result = await handleAttachmentTool(
			"list_task_attachments",
			{ projectId: "p1", taskId: "t1" },
			makeClient(),
		);
		expect(result.content[0].text).toContain("photo.png");
	});

	it("throws ZodError when projectId is missing", async () => {
		await expect(
			handleAttachmentTool(
				"list_task_attachments",
				{ taskId: "t1" },
				makeClient(),
			),
		).rejects.toThrow();
	});
});

// ---------------------------------------------------------------------------
// get_attachment_download_url
// ---------------------------------------------------------------------------

describe("handleAttachmentTool – get_attachment_download_url", () => {
	it("calls viewsClient.getAttachmentDownloadURL with all three IDs", async () => {
		const client = makeClient();
		await handleAttachmentTool(
			"get_attachment_download_url",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-1" },
			client,
		);
		expect(client.getAttachmentDownloadURL).toHaveBeenCalledWith(
			"p1",
			"t1",
			"att-1",
		);
	});

	it("returns the download URL in the response text", async () => {
		const result = await handleAttachmentTool(
			"get_attachment_download_url",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-1" },
			makeClient(),
		);
		expect(result.content[0].text).toContain(
			"https://cdn.example.com/photo.png",
		);
	});
});

// ---------------------------------------------------------------------------
// read_task_attachment
// ---------------------------------------------------------------------------

describe("handleAttachmentTool – read_task_attachment", () => {
	it("returns an image content block for an image attachment", async () => {
		const client = makeClient();
		const result = await handleAttachmentTool(
			"read_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-1" },
			client,
		);
		expect(client.downloadAttachmentContent).toHaveBeenCalledWith(
			"p1",
			"t1",
			"att-1",
		);
		expect(result.content[0].type).toBe("image");
		expect(result.content[0].mimeType).toBe("image/png");
		expect(result.content[0].data).toBe(
			Buffer.from("hello world").toString("base64"),
		);
	});

	it("returns a text content block for a text attachment", async () => {
		const textAttachment = {
			id: "att-2",
			file: {
				file_name: "notes.md",
				file_size: 11,
				content_type: "text/markdown",
			},
			created_by: "user-1",
			created_at: "2024-01-01T00:00:00Z",
		};
		const client = makeClient({
			listTaskAttachments: vi.fn().mockResolvedValue([textAttachment]),
			downloadAttachmentContent: vi.fn().mockResolvedValue({
				buffer: new TextEncoder().encode("hello world").buffer,
				contentType: "text/markdown",
			}),
		});
		const result = await handleAttachmentTool(
			"read_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-2" },
			client,
		);
		expect(result.content[0].type).toBe("text");
		expect(result.content[0].text).toContain("notes.md");
		expect(result.content[0].text).toContain("hello world");
	});

	it("classifies a generic content-type by file extension", async () => {
		const codeAttachment = {
			id: "att-3",
			file: {
				file_name: "script.py",
				file_size: 11,
				content_type: "application/octet-stream",
			},
			created_by: "user-1",
			created_at: "2024-01-01T00:00:00Z",
		};
		const client = makeClient({
			listTaskAttachments: vi.fn().mockResolvedValue([codeAttachment]),
			downloadAttachmentContent: vi.fn().mockResolvedValue({
				buffer: new TextEncoder().encode("print('hi')").buffer,
				contentType: "application/octet-stream",
			}),
		});
		const result = await handleAttachmentTool(
			"read_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-3" },
			client,
		);
		expect(result.content[0].type).toBe("text");
		expect(result.content[0].text).toContain("print('hi')");
	});

	it("classifies a generic content-type by conventional extensionless filename", async () => {
		const dockerfileAttachment = {
			id: "att-3b",
			file: {
				file_name: "Dockerfile",
				file_size: 11,
				content_type: "application/octet-stream",
			},
			created_by: "user-1",
			created_at: "2024-01-01T00:00:00Z",
		};
		const client = makeClient({
			listTaskAttachments: vi.fn().mockResolvedValue([dockerfileAttachment]),
			downloadAttachmentContent: vi.fn().mockResolvedValue({
				buffer: new TextEncoder().encode("FROM node:20").buffer,
				contentType: "application/octet-stream",
			}),
		});
		const result = await handleAttachmentTool(
			"read_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-3b" },
			client,
		);
		expect(result.content[0].type).toBe("text");
		expect(result.content[0].text).toContain("FROM node:20");
	});

	it("returns an error for a binary attachment it can't read", async () => {
		const pdfAttachment = {
			id: "att-4",
			file: {
				file_name: "report.pdf",
				file_size: 1024,
				content_type: "application/pdf",
			},
			created_by: "user-1",
			created_at: "2024-01-01T00:00:00Z",
		};
		const client = makeClient({
			listTaskAttachments: vi.fn().mockResolvedValue([pdfAttachment]),
		});
		const result = await handleAttachmentTool(
			"read_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-4" },
			client,
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("get_attachment_download_url");
		expect(client.downloadAttachmentContent).not.toHaveBeenCalled();
	});

	it("returns an error when the attachment exceeds the size limit", async () => {
		const bigAttachment = {
			id: "att-5",
			file: {
				file_name: "huge.txt",
				file_size: 3 * 1024 * 1024,
				content_type: "text/plain",
			},
			created_by: "user-1",
			created_at: "2024-01-01T00:00:00Z",
		};
		const client = makeClient({
			listTaskAttachments: vi.fn().mockResolvedValue([bigAttachment]),
		});
		const result = await handleAttachmentTool(
			"read_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-5" },
			client,
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("exceeds");
		expect(client.downloadAttachmentContent).not.toHaveBeenCalled();
	});

	it("returns an error when the attachment isn't found", async () => {
		const client = makeClient();
		const result = await handleAttachmentTool(
			"read_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "does-not-exist" },
			client,
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("not found");
	});
});

// ---------------------------------------------------------------------------
// delete_task_attachment
// ---------------------------------------------------------------------------

describe("handleAttachmentTool – delete_task_attachment", () => {
	it("calls viewsClient.deleteTaskAttachment with all three IDs", async () => {
		const client = makeClient();
		await handleAttachmentTool(
			"delete_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-1" },
			client,
		);
		expect(client.deleteTaskAttachment).toHaveBeenCalledWith(
			"p1",
			"t1",
			"att-1",
		);
	});

	it("includes 'deleted successfully' in the response", async () => {
		const result = await handleAttachmentTool(
			"delete_task_attachment",
			{ projectId: "p1", taskId: "t1", attachmentId: "att-1" },
			makeClient(),
		);
		expect(result.content[0].text).toContain("deleted successfully");
		expect(result.content[0].text).toContain("att-1");
	});
});

// ---------------------------------------------------------------------------
// unknown tool
// ---------------------------------------------------------------------------

describe("handleAttachmentTool – unknown tool", () => {
	it("throws for an unknown tool name", async () => {
		await expect(
			handleAttachmentTool("bad_tool", {}, makeClient()),
		).rejects.toThrow("Unknown attachment tool");
	});
});

// ---------------------------------------------------------------------------
// read_doc_file
// ---------------------------------------------------------------------------

const MAX_TEXT_BYTES = 2 * 1024 * 1024;
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const DOC_FILE_ARGS = { projectId: "p1", docId: "d1", fileId: "f1" };

function makeDownload(overrides: Record<string, any> = {}) {
	return {
		contentType: "image/png",
		contentLength: 11,
		fileName: "screenshot.png",
		read: vi
			.fn()
			.mockResolvedValue(new TextEncoder().encode("hello world").buffer),
		discard: vi.fn().mockResolvedValue(undefined),
		...overrides,
	};
}

function makeDocClient(download: any = makeDownload()) {
	return { openDocFile: vi.fn().mockResolvedValue(download) } as any;
}

describe("handleDocFileTool – read_doc_file", () => {
	it("returns an image content block for an image file", async () => {
		const download = makeDownload();
		const client = makeDocClient(download);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client,
		);
		expect(client.openDocFile).toHaveBeenCalledWith("p1", "d1", "f1");
		expect(download.read).toHaveBeenCalledWith(MAX_IMAGE_BYTES);
		expect(result.isError).toBeFalsy();
		expect(result.content[0].type).toBe("image");
		expect(result.content[0].mimeType).toBe("image/png");
		expect(result.content[0].data).toBe(
			Buffer.from("hello world").toString("base64"),
		);
	});

	it("strips content-type parameters from the image mimeType", async () => {
		const client = makeDocClient(
			makeDownload({ contentType: "Image/JPEG; charset=binary" }),
		);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client,
		);
		expect(result.content[0].type).toBe("image");
		expect(result.content[0].mimeType).toBe("image/jpeg");
	});

	it("returns a text content block for a text file", async () => {
		const download = makeDownload({
			contentType: "text/markdown",
			fileName: "notes.md",
		});
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			makeDocClient(download),
		);
		expect(download.read).toHaveBeenCalledWith(MAX_TEXT_BYTES);
		expect(result.content[0].type).toBe("text");
		expect(result.content[0].text).toContain("notes.md");
		expect(result.content[0].text).toContain("hello world");
	});

	it("classifies a generic content-type by file name", async () => {
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			makeDocClient(
				makeDownload({
					contentType: "application/octet-stream",
					fileName: "script.py",
				}),
			),
		);
		expect(result.content[0].type).toBe("text");
		expect(result.content[0].text).toContain("hello world");
	});

	it("rejects a binary file without reading its body", async () => {
		const download = makeDownload({
			contentType: "application/pdf",
			fileName: "report.pdf",
			contentLength: 1024,
		});
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			makeDocClient(download),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("report.pdf");
		expect(result.content[0].text).toContain("can't be read");
		expect(download.read).not.toHaveBeenCalled();
		expect(download.discard).toHaveBeenCalled();
	});

	it("rejects a file whose Content-Length exceeds the limit without reading its body", async () => {
		const download = makeDownload({
			contentType: "text/plain",
			fileName: "huge.txt",
			contentLength: 3 * 1024 * 1024,
		});
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			makeDocClient(download),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("exceeds");
		expect(download.read).not.toHaveBeenCalled();
		expect(download.discard).toHaveBeenCalled();
	});

	it("rejects a file whose body turns out larger than the limit", async () => {
		const download = makeDownload({
			contentLength: undefined,
			read: vi.fn().mockResolvedValue(null),
		});
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			makeDocClient(download),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("exceeds");
	});

	it("surfaces an API error as an isError result instead of rejecting", async () => {
		const client = {
			openDocFile: vi
				.fn()
				.mockRejectedValue(
					new Error("Permission denied: missing permission docs.read"),
				),
		} as any;
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client,
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("Permission denied");
	});

	it("returns an isError result when fileId is missing", async () => {
		const client = makeDocClient();
		const result = await handleDocFileTool(
			"read_doc_file",
			{ projectId: "p1", docId: "d1" },
			client,
		);
		expect(result.isError).toBe(true);
		expect(client.openDocFile).not.toHaveBeenCalled();
	});

	it("returns an isError result for an unknown tool name", async () => {
		const result = await handleDocFileTool("bad_tool", {}, makeDocClient());
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("Unknown doc file tool");
	});
});

// ---------------------------------------------------------------------------
// read_doc_file through the real PacaAPIDocClient (fetch stubbed)
// ---------------------------------------------------------------------------

describe("read_doc_file – download through PacaAPIDocClient", () => {
	const DOWNLOAD_URL_PATH = "/api/v1/projects/p1/docs/d1/files/f1/download-url";
	const STORAGE_URL =
		"https://storage.example.com/paca/docs/d1/f1/screenshot.png?X-Amz-Signature=abc";
	let fetchMock: ReturnType<typeof vi.fn>;

	function downloadURLResponse(url: string) {
		return {
			ok: true,
			status: 200,
			json: async () => ({ success: true, data: { url } }),
			text: async () => "",
		};
	}

	function client() {
		return new PacaAPIDocClient({
			baseURL: "https://api.example.com",
			apiKey: "key123",
		});
	}

	beforeEach(() => {
		fetchMock = vi.fn();
		vi.stubGlobal("fetch", fetchMock);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it("gets the presigned URL from the API, then downloads it without the API key", async () => {
		fetchMock
			.mockResolvedValueOnce(downloadURLResponse(STORAGE_URL))
			.mockResolvedValueOnce(
				new Response(new Uint8Array([1, 2, 3]), {
					headers: { "content-type": "image/png", "content-length": "3" },
				}),
			);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(fetchMock.mock.calls[0][0]).toBe(
			`https://api.example.com${DOWNLOAD_URL_PATH}`,
		);
		expect(fetchMock.mock.calls[0][1].headers["X-API-Key"]).toBe("key123");
		expect(fetchMock.mock.calls[1]).toEqual([STORAGE_URL]);
		expect(result.content[0].type).toBe("image");
		expect(result.content[0].mimeType).toBe("image/png");
		expect(result.content[0].data).toBe(
			Buffer.from([1, 2, 3]).toString("base64"),
		);
	});

	it("takes the file name from the presigned URL when there's no Content-Disposition", async () => {
		fetchMock
			.mockResolvedValueOnce(
				downloadURLResponse(
					"https://storage.example.com/paca/docs/d1/f1/notes.md?X-Amz-Signature=abc",
				),
			)
			.mockResolvedValueOnce(
				new Response("# Notes", {
					headers: { "content-type": "application/octet-stream" },
				}),
			);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(result.content[0].type).toBe("text");
		expect(result.content[0].text).toContain("File: notes.md");
		expect(result.content[0].text).toContain("# Notes");
	});

	it("prefers the Content-Disposition file name", async () => {
		fetchMock
			.mockResolvedValueOnce(
				downloadURLResponse("https://storage.example.com/paca/docs/d1/f1/file"),
			)
			.mockResolvedValueOnce(
				new Response("line 1", {
					headers: {
						"content-type": "application/octet-stream",
						"content-disposition": 'attachment; filename="build.log"',
					},
				}),
			);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(result.content[0].type).toBe("text");
		expect(result.content[0].text).toContain("File: build.log");
	});

	it("rejects an oversized Content-Length without reading the body", async () => {
		const body = {
			getReader: vi.fn(),
			cancel: vi.fn().mockResolvedValue(undefined),
		};
		fetchMock
			.mockResolvedValueOnce(downloadURLResponse(STORAGE_URL))
			.mockResolvedValueOnce({
				ok: true,
				status: 200,
				headers: new Headers({
					"content-type": "image/png",
					"content-length": String(MAX_IMAGE_BYTES + 1),
				}),
				body,
			});
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("exceeds");
		expect(body.getReader).not.toHaveBeenCalled();
		expect(body.cancel).toHaveBeenCalled();
	});

	it("stops reading a body with no Content-Length once it passes the limit", async () => {
		let pulls = 0;
		const endless = new ReadableStream<Uint8Array>({
			pull(controller) {
				pulls++;
				controller.enqueue(new Uint8Array(1024 * 1024));
			},
		});
		fetchMock
			.mockResolvedValueOnce(downloadURLResponse(STORAGE_URL))
			.mockResolvedValueOnce(
				new Response(endless, { headers: { "content-type": "text/plain" } }),
			);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("exceeds");
		// 2 MB limit in 1 MB chunks: a handful of pulls, never an unbounded read.
		expect(pulls).toBeLessThan(10);
	});

	it("surfaces the API's error and never contacts object storage", async () => {
		fetchMock.mockResolvedValueOnce({
			ok: false,
			status: 404,
			statusText: "Not Found",
			text: async () =>
				'{"error":"file does not belong to the specified document"}',
		});
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain(
			"file does not belong to the specified document",
		);
		expect(fetchMock).toHaveBeenCalledTimes(1);
	});

	it("surfaces an object storage error", async () => {
		fetchMock
			.mockResolvedValueOnce(downloadURLResponse(STORAGE_URL))
			.mockResolvedValueOnce(
				new Response("<Error><Code>AccessDenied</Code></Error>", {
					status: 403,
					statusText: "Forbidden",
				}),
			);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("object storage failed");
		expect(result.content[0].text).toContain("403");
	});

	it("names the network error when object storage is unreachable", async () => {
		fetchMock
			.mockResolvedValueOnce(downloadURLResponse(STORAGE_URL))
			.mockRejectedValueOnce(
				new TypeError("fetch failed", {
					cause: new Error("getaddrinfo ENOTFOUND storage.example.com"),
				}),
			);
		const result = await handleDocFileTool(
			"read_doc_file",
			DOC_FILE_ARGS,
			client(),
		);
		expect(result.isError).toBe(true);
		expect(result.content[0].text).toContain("Could not reach object storage");
		expect(result.content[0].text).toContain("ENOTFOUND");
	});
});
