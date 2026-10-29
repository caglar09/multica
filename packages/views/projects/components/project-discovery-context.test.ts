import { describe, expect, it } from "vitest";
import { collectProjectContext } from "./project-discovery-context";

function selectedFile(path: string, content: string) {
	const bytes = new TextEncoder().encode(content);
	const parts = path.split("/");
	return {
		name: parts.at(-1) ?? path,
		size: bytes.byteLength,
		webkitRelativePath: path,
		arrayBuffer: async () => bytes.buffer,
	};
}

describe("collectProjectContext", () => {
	it("collects arbitrary text paths and excludes secrets, generated files, and binary content", async () => {
		const context = await collectProjectContext([
			selectedFile("sample/odd-layout/logic.src", "source"),
			selectedFile("sample/.env", "TOKEN=secret"),
			selectedFile("sample/node_modules/pkg/index.js", "dependency"),
			selectedFile("sample/image.dat", "\u0000binary"),
		]);

		expect(context.root_name).toBe("sample");
		expect(context.files).toEqual([{ path: "odd-layout/logic.src", content: "source" }]);
		expect(context.warnings).toHaveLength(2);
	});

	it("rejects a selection with no readable text files", async () => {
		await expect(collectProjectContext([selectedFile("sample/.env", "secret")]))
			.rejects.toThrow("no readable text files");
	});
});
