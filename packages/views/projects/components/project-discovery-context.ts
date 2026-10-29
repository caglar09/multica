import type { ProjectContextPayload } from "@multica/core/types";

const maxFiles = 256;
const maxFileBytes = 256 * 1024;
const maxTotalBytes = 4 * 1024 * 1024;
const excludedDirectories = new Set([
	".git", ".multica", ".svn", ".hg", "node_modules", "vendor", "dist", "build",
	"target", "out", ".next", ".nuxt", "coverage", ".cache", ".turbo", ".venv",
	"venv", "__pycache__", ".ssh", ".aws", ".kube", "credentials", "secrets",
]);
const sensitiveFile = /(^|\/)\.env($|\.)|(^|\/)(credentials?|secrets?)(\.|$)|(^|\/)(id_rsa|id_ed25519|\.netrc|\.npmrc|\.pypirc|\.dockerconfigjson|kubeconfig)($|\.)|\.(pem|key|p12|pfx|tfstate)$/i;

interface ProjectFileLike {
	name: string;
	size: number;
	webkitRelativePath?: string;
	arrayBuffer(): Promise<ArrayBuffer>;
}

export async function collectProjectContext(files: ArrayLike<ProjectFileLike>): Promise<ProjectContextPayload> {
	const context: ProjectContextPayload = { root_name: "Selected project", files: [], warnings: [] };
	let totalBytes = 0;
	for (let index = 0; index < files.length; index += 1) {
		const file = files[index];
		if (!file) continue;
		const fullPath = (file.webkitRelativePath || file.name).replaceAll("\\", "/");
		const parts = fullPath.split("/").filter(Boolean);
		const rootName = file.webkitRelativePath && parts.length > 1 ? (parts[0] ?? "Selected project") : "Selected project";
		const path = file.webkitRelativePath && parts.length > 1 ? parts.slice(1).join("/") : parts.join("/");
		if (context.root_name === "Selected project" && rootName !== "Selected project") context.root_name = rootName;
		if (!path || path.startsWith("/") || path.split("/").some((part) => part === "..")) {
			throw new Error("The selected folder contains an invalid path.");
		}
		const directories = path.split("/").slice(0, -1);
		if (directories.some((part) => excludedDirectories.has(part.toLowerCase())) || sensitiveFile.test(path)) {
			context.warnings?.push("Sensitive and generated files were excluded from project analysis.");
			continue;
		}
		if (file.size > maxFileBytes) {
			context.warnings?.push("Large files were excluded from project analysis.");
			continue;
		}
		if (context.files.length >= maxFiles || totalBytes + file.size > maxTotalBytes) {
			context.warnings?.push("Project analysis input was truncated to stay within safe size limits.");
			break;
		}
		try {
			const bytes = new Uint8Array(await file.arrayBuffer());
			if (bytes.includes(0)) {
				context.warnings?.push("Binary and non-text files were excluded from project analysis.");
				continue;
			}
			const content = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
			context.files.push({ path, content });
			totalBytes += bytes.byteLength;
		} catch {
			context.warnings?.push("Binary and non-text files were excluded from project analysis.");
		}
	}
	if (context.files.length === 0) throw new Error("The selected folder contains no readable text files.");
	context.files.sort((a, b) => a.path.localeCompare(b.path));
	context.warnings = [...new Set(context.warnings)];
	return context;
}
