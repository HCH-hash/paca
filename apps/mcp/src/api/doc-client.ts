import type {
	CreateFolderInput,
	DocumentActivity,
	DocumentFolder,
	DocumentSnapshot,
	PacaConfig,
	SuccessEnvelope,
	UpdateFolderInput,
} from "../types/index.js";
import { formatApiRequestError, markdownToBlocknote } from "../utils/index.js";

/**
 * A doc file download whose response headers have arrived but whose body has
 * not been read yet — see PacaAPIDocClient.openDocFile.
 */
export interface DocFileDownload {
	/** Content type the object store serves the file with (the one given at upload). */
	contentType?: string;
	/** Size in bytes, from Content-Length when the response carries one. */
	contentLength?: number;
	/**
	 * File name from Content-Disposition when present, else the presigned
	 * URL's last path segment — doc files are stored under
	 * docs/{docId}/{fileId}/{fileName}, so that segment is the uploaded name.
	 */
	fileName?: string;
	/**
	 * Reads the body, resolving null — and cancelling the rest of the
	 * download — as soon as more than maxBytes have arrived, so a missing or
	 * understated Content-Length can never make this buffer an unbounded body.
	 */
	read(maxBytes: number): Promise<ArrayBuffer | null>;
	/** Releases the response without reading its body. */
	discard(): Promise<void>;
}

/**
 * Extended API client for Document features.
 */
export class PacaAPIDocClient {
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
				formatApiRequestError(response.status, response.statusText, errorText),
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

	private async post(path: string, body: any): Promise<any> {
		return this.request("POST", path, body);
	}

	private async patch(path: string, body: any): Promise<any> {
		return this.request("PATCH", path, body);
	}

	private async delete(path: string): Promise<any> {
		return this.request("DELETE", path);
	}

	// ==================== Document Folders ====================

	async listFolders(projectId: string): Promise<DocumentFolder[]> {
		const response = await this.get(
			`/api/v1/projects/${projectId}/docs/folders`,
		);
		if (Array.isArray(response)) {
			return response;
		}
		return response.items || response.folders || response.data || [];
	}

	async createFolder(
		projectId: string,
		input: CreateFolderInput,
	): Promise<DocumentFolder> {
		return this.post(`/api/v1/projects/${projectId}/docs/folders`, input);
	}

	async updateFolder(
		projectId: string,
		folderId: string,
		input: UpdateFolderInput,
	): Promise<DocumentFolder> {
		return this.patch(
			`/api/v1/projects/${projectId}/docs/folders/${folderId}`,
			input,
		);
	}

	async deleteFolder(projectId: string, folderId: string): Promise<void> {
		await this.delete(`/api/v1/projects/${projectId}/docs/folders/${folderId}`);
	}

	// ==================== Document Snapshots ====================

	async listSnapshots(
		projectId: string,
		docId: string,
	): Promise<DocumentSnapshot[]> {
		const response = await this.get(
			`/api/v1/projects/${projectId}/docs/${docId}/snapshots`,
		);
		if (Array.isArray(response)) {
			return response;
		}
		return response.items || response.snapshots || response.data || [];
	}

	async getSnapshot(
		projectId: string,
		docId: string,
		snapshotId: string,
	): Promise<DocumentSnapshot> {
		return this.get(
			`/api/v1/projects/${projectId}/docs/${docId}/snapshots/${snapshotId}`,
		);
	}

	// ==================== Document Activities ====================

	async listDocumentActivities(
		projectId: string,
		docId: string,
	): Promise<DocumentActivity[]> {
		const response = await this.get(
			`/api/v1/projects/${projectId}/docs/${docId}/activities`,
		);
		if (Array.isArray(response)) {
			return response;
		}
		return response.items || response.activities || response.data || [];
	}

	// ==================== Document Comments ====================

	async addDocumentComment(
		projectId: string,
		docId: string,
		content: string,
	): Promise<DocumentActivity> {
		const contentBlocks = content ? markdownToBlocknote(content) : null;
		return this.post(`/api/v1/projects/${projectId}/docs/${docId}/comments`, {
			content: contentBlocks,
		});
	}

	async updateDocumentComment(
		projectId: string,
		docId: string,
		commentId: string,
		content: string,
	): Promise<DocumentActivity> {
		const contentBlocks = content ? markdownToBlocknote(content) : null;
		return this.patch(
			`/api/v1/projects/${projectId}/docs/${docId}/comments/${commentId}`,
			{ content: contentBlocks },
		);
	}

	async deleteDocumentComment(
		projectId: string,
		docId: string,
		commentId: string,
	): Promise<void> {
		await this.delete(
			`/api/v1/projects/${projectId}/docs/${docId}/comments/${commentId}`,
		);
	}

	// ==================== Document Files ====================

	async getDocFileDownloadURL(
		projectId: string,
		docId: string,
		fileId: string,
	): Promise<string> {
		const response = await this.get(
			`/api/v1/projects/${projectId}/docs/${docId}/files/${fileId}/download-url`,
		);
		return response.url || response.downloadUrl || "";
	}

	/**
	 * Opens a doc file for reading, resolving once the response headers have
	 * arrived and before any of the body is read — so the caller can decide
	 * from the content type and size whether the body is worth reading at all.
	 * Doc files have no metadata endpoint, so these headers are the only
	 * source for either.
	 *
	 * Unlike task attachments (see PacaAPIViewsClient.downloadAttachmentContent),
	 * doc files have no API endpoint that streams their bytes, so this fetches
	 * the presigned object-store URL from getDocFileDownloadURL. That API route
	 * is also what checks the file belongs to the doc, the doc to the project,
	 * and that the caller may read docs. The URL points at the object store's
	 * public host (the backend's STORAGE_PUBLIC_URL); a caller that can't reach
	 * it gets a descriptive error rather than a silent failure. The API key is
	 * deliberately not sent there — the presigned URL carries its own
	 * authorization.
	 */
	async openDocFile(
		projectId: string,
		docId: string,
		fileId: string,
	): Promise<DocFileDownload> {
		const url = await this.getDocFileDownloadURL(projectId, docId, fileId);
		if (!url) {
			throw new Error(
				`The API returned no download URL for doc file ${fileId}.`,
			);
		}

		let response: Response;
		try {
			response = await fetch(url);
		} catch (err) {
			throw new Error(
				`Could not reach object storage to download doc file ${fileId}: ${describeFetchError(err)}`,
			);
		}
		if (!response.ok) {
			const errorText = await response.text().catch(() => "");
			throw new Error(
				`Downloading doc file ${fileId} from object storage failed: ${formatApiRequestError(
					response.status,
					response.statusText,
					errorText,
				)}`,
			);
		}

		return {
			contentType: response.headers.get("content-type") ?? undefined,
			contentLength: parseContentLength(
				response.headers.get("content-length"),
			),
			fileName:
				fileNameFromContentDisposition(
					response.headers.get("content-disposition"),
				) ?? fileNameFromURL(url),
			read: (maxBytes) => readBodyUpTo(response, maxBytes),
			discard: async () => {
				await response.body?.cancel().catch(() => undefined);
			},
		};
	}

	async deleteDocFile(
		projectId: string,
		docId: string,
		fileId: string,
	): Promise<void> {
		await this.delete(
			`/api/v1/projects/${projectId}/docs/${docId}/files/${fileId}`,
		);
	}
}

// ==================== Doc file download helpers ====================

/** Names the underlying network error, which fetch wraps as "fetch failed". */
function describeFetchError(err: unknown): string {
	if (!(err instanceof Error)) return String(err);
	return err.cause instanceof Error
		? `${err.message} (${err.cause.message})`
		: err.message;
}

function parseContentLength(header: string | null): number | undefined {
	if (!header) return undefined;
	const length = Number(header);
	return Number.isSafeInteger(length) && length >= 0 ? length : undefined;
}

function safeDecodeURIComponent(value: string): string | undefined {
	try {
		return decodeURIComponent(value);
	} catch {
		return undefined;
	}
}

function fileNameFromContentDisposition(
	header: string | null,
): string | undefined {
	if (!header) return undefined;
	// Prefer the RFC 5987 form (filename*=UTF-8''name) over plain filename=.
	const extended = /filename\*\s*=\s*[^']*'[^']*'([^;]+)/i.exec(header);
	if (extended) {
		const decoded = safeDecodeURIComponent(extended[1].trim());
		if (decoded) return decoded;
	}
	const plain = /filename\s*=\s*(?:"([^"]*)"|([^;]+))/i.exec(header);
	const name = (plain?.[1] ?? plain?.[2])?.trim();
	return name || undefined;
}

function fileNameFromURL(url: string): string | undefined {
	try {
		const segment = new URL(url).pathname.split("/").pop();
		return segment ? safeDecodeURIComponent(segment) : undefined;
	} catch {
		return undefined;
	}
}

/**
 * Reads a response body into memory, giving up (and cancelling the stream)
 * as soon as more than maxBytes have arrived. Resolves null in that case.
 */
async function readBodyUpTo(
	response: Response,
	maxBytes: number,
): Promise<ArrayBuffer | null> {
	if (!response.body) return new ArrayBuffer(0);
	const reader = response.body.getReader();
	const chunks: Uint8Array[] = [];
	let total = 0;
	let chunk = await reader.read();
	while (!chunk.done) {
		total += chunk.value.byteLength;
		if (total > maxBytes) {
			await reader.cancel().catch(() => undefined);
			return null;
		}
		chunks.push(chunk.value);
		chunk = await reader.read();
	}
	const bytes = new Uint8Array(total);
	let offset = 0;
	for (const part of chunks) {
		bytes.set(part, offset);
		offset += part.byteLength;
	}
	return bytes.buffer;
}
