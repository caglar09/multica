#!/usr/bin/env node

import {
  closeSync,
  existsSync,
  mkdtempSync,
  openSync,
  readSync,
  rmSync,
  statSync,
  writeFileSync,
  writeSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const ZIP_LIMIT = 0xffffffff;
const LOG_TAIL_LINES = "50000";
const DEFAULT_PROFILE = "selfhost";

function fail(message) {
  console.error(`\n[error] ${message}`);
  process.exitCode = 1;
}

function info(message) {
  console.log(`[support-bundle] ${message}`);
}

function parseArgs(argv) {
  let output;
  let profile = DEFAULT_PROFILE;

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--output") {
      output = argv[++i];
      if (!output) throw new Error("--output requires a file path");
      continue;
    }
    if (arg === "--profile") {
      profile = argv[++i];
      if (!profile) throw new Error("--profile requires a value");
      continue;
    }
    if (arg === "--help" || arg === "-h") {
      console.log(`Usage: pnpm support:bundle [-- --output <file.zip>] [--profile <name>]\n\nCollects a PostgreSQL custom-format dump, self-host container logs, daemon diagnostics, and basic Docker/Git metadata into one ZIP file.\n\nDefaults:\n  --profile ${DEFAULT_PROFILE}\n  --output ./multica-support-bundle-<timestamp>.zip`);
      process.exit(0);
    }
    throw new Error(`Unknown argument: ${arg}`);
  }

  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  return {
    output: resolve(output ?? `multica-support-bundle-${stamp}.zip`),
    profile,
  };
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd ?? process.cwd(),
    encoding: "utf8",
    env: process.env,
    maxBuffer: options.maxBuffer ?? 8 * 1024 * 1024,
  });

  if (result.error) {
    if (options.allowFailure) {
      return { ok: false, stdout: "", stderr: result.error.message, status: null };
    }
    throw result.error;
  }

  const ok = result.status === 0;
  if (!ok && !options.allowFailure) {
    const detail = (result.stderr || result.stdout || "unknown error").trim();
    throw new Error(`${command} ${args.join(" ")} failed: ${detail}`);
  }

  return {
    ok,
    stdout: result.stdout ?? "",
    stderr: result.stderr ?? "",
    status: result.status,
  };
}

function runToFile(command, args, filePath, { allowFailure = true } = {}) {
  const fd = openSync(filePath, "w");
  try {
    const result = spawnSync(command, args, {
      stdio: ["ignore", fd, fd],
      env: process.env,
      cwd: process.cwd(),
    });
    if (result.error) {
      writeSync(fd, `\n${result.error.message}\n`);
      if (!allowFailure) throw result.error;
      return false;
    }
    if (result.status !== 0 && !allowFailure) {
      throw new Error(`${command} ${args.join(" ")} exited with status ${result.status}`);
    }
    return result.status === 0;
  } finally {
    closeSync(fd);
  }
}

function runBinaryToFile(command, args, filePath) {
  const fd = openSync(filePath, "w");
  try {
    const result = spawnSync(command, args, {
      stdio: ["ignore", fd, "pipe"],
      encoding: "utf8",
      env: process.env,
      cwd: process.cwd(),
      maxBuffer: 8 * 1024 * 1024,
    });
    if (result.error) throw result.error;
    if (result.status !== 0) {
      const detail = (result.stderr || "unknown error").trim();
      throw new Error(`${command} ${args.join(" ")} failed: ${detail}`);
    }
  } finally {
    closeSync(fd);
  }
}

function composeArgs() {
  const args = ["compose", "-f", "docker-compose.selfhost.yml"];
  if (existsSync("docker-compose.selfhost.build.yml")) {
    args.push("-f", "docker-compose.selfhost.build.yml");
  }
  return args;
}

function composeContainerId(service) {
  const result = run("docker", [...composeArgs(), "ps", "-q", service]);
  const id = result.stdout.trim();
  if (!id) throw new Error(`Self-host service '${service}' is not running`);
  return id;
}

function containerName(id) {
  const result = run("docker", ["inspect", "--format", "{{.Name}}", id], { allowFailure: true });
  return result.ok ? result.stdout.trim().replace(/^\//, "") : id;
}

function containerEnv(id, key, fallback) {
  const result = run("docker", ["exec", id, "sh", "-lc", `printenv ${key}`], { allowFailure: true });
  const value = result.stdout.trim();
  return value || fallback;
}

function writeCommandResult(filePath, command, args) {
  const result = run(command, args, { allowFailure: true, maxBuffer: 16 * 1024 * 1024 });
  const body = [
    `$ ${command} ${args.join(" ")}`,
    "",
    result.stdout.trimEnd(),
    result.stderr ? `\n[stderr]\n${result.stderr.trimEnd()}` : "",
    `\n[exit] ${result.status ?? "spawn-error"}`,
    "",
  ].join("\n");
  writeFileSync(filePath, body);
  return result.ok;
}

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n += 1) {
    let c = n;
    for (let k = 0; k < 8; k += 1) {
      c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    }
    table[n] = c >>> 0;
  }
  return table;
})();

function crc32Update(crc, chunk) {
  let c = crc;
  for (let i = 0; i < chunk.length; i += 1) {
    c = CRC_TABLE[(c ^ chunk[i]) & 0xff] ^ (c >>> 8);
  }
  return c >>> 0;
}

function dosDateTime(date) {
  const year = Math.min(2107, Math.max(1980, date.getFullYear()));
  const month = date.getMonth() + 1;
  const day = date.getDate();
  const hours = date.getHours();
  const minutes = date.getMinutes();
  const seconds = Math.floor(date.getSeconds() / 2);
  return {
    date: ((year - 1980) << 9) | (month << 5) | day,
    time: (hours << 11) | (minutes << 5) | seconds,
  };
}

function createZip(outputPath, files) {
  const outFd = openSync(outputPath, "w");
  const entries = [];
  let offset = 0;

  const writeBuffer = (buffer) => {
    writeSync(outFd, buffer);
    offset += buffer.length;
    if (offset > ZIP_LIMIT) throw new Error("Support bundle exceeded the classic ZIP 4 GiB limit");
  };

  try {
    for (const file of files) {
      const stat = statSync(file.path);
      if (stat.size > ZIP_LIMIT) throw new Error(`${file.name} exceeds the classic ZIP 4 GiB file limit`);

      const name = Buffer.from(file.name, "utf8");
      const { date, time } = dosDateTime(stat.mtime);
      const localOffset = offset;
      const flags = 0x0808; // data descriptor + UTF-8 names

      const header = Buffer.alloc(30);
      header.writeUInt32LE(0x04034b50, 0);
      header.writeUInt16LE(20, 4);
      header.writeUInt16LE(flags, 6);
      header.writeUInt16LE(0, 8); // stored, no compression
      header.writeUInt16LE(time, 10);
      header.writeUInt16LE(date, 12);
      header.writeUInt32LE(0, 14);
      header.writeUInt32LE(0, 18);
      header.writeUInt32LE(0, 22);
      header.writeUInt16LE(name.length, 26);
      header.writeUInt16LE(0, 28);
      writeBuffer(header);
      writeBuffer(name);

      const inFd = openSync(file.path, "r");
      const buffer = Buffer.allocUnsafe(1024 * 1024);
      let crc = 0xffffffff;
      let size = 0;
      try {
        while (true) {
          const bytesRead = readSync(inFd, buffer, 0, buffer.length, null);
          if (bytesRead === 0) break;
          const chunk = buffer.subarray(0, bytesRead);
          crc = crc32Update(crc, chunk);
          writeBuffer(chunk);
          size += bytesRead;
        }
      } finally {
        closeSync(inFd);
      }
      crc = (crc ^ 0xffffffff) >>> 0;

      const descriptor = Buffer.alloc(16);
      descriptor.writeUInt32LE(0x08074b50, 0);
      descriptor.writeUInt32LE(crc, 4);
      descriptor.writeUInt32LE(size, 8);
      descriptor.writeUInt32LE(size, 12);
      writeBuffer(descriptor);

      entries.push({ name, crc, size, time, date, flags, localOffset });
    }

    const centralOffset = offset;
    for (const entry of entries) {
      const header = Buffer.alloc(46);
      header.writeUInt32LE(0x02014b50, 0);
      header.writeUInt16LE(20, 4);
      header.writeUInt16LE(20, 6);
      header.writeUInt16LE(entry.flags, 8);
      header.writeUInt16LE(0, 10);
      header.writeUInt16LE(entry.time, 12);
      header.writeUInt16LE(entry.date, 14);
      header.writeUInt32LE(entry.crc, 16);
      header.writeUInt32LE(entry.size, 20);
      header.writeUInt32LE(entry.size, 24);
      header.writeUInt16LE(entry.name.length, 28);
      header.writeUInt16LE(0, 30);
      header.writeUInt16LE(0, 32);
      header.writeUInt16LE(0, 34);
      header.writeUInt16LE(0, 36);
      header.writeUInt32LE(0, 38);
      header.writeUInt32LE(entry.localOffset, 42);
      writeBuffer(header);
      writeBuffer(entry.name);
    }

    const centralSize = offset - centralOffset;
    if (entries.length > 0xffff) throw new Error("Too many files for classic ZIP format");

    const end = Buffer.alloc(22);
    end.writeUInt32LE(0x06054b50, 0);
    end.writeUInt16LE(0, 4);
    end.writeUInt16LE(0, 6);
    end.writeUInt16LE(entries.length, 8);
    end.writeUInt16LE(entries.length, 10);
    end.writeUInt32LE(centralSize, 12);
    end.writeUInt32LE(centralOffset, 16);
    end.writeUInt16LE(0, 20);
    writeBuffer(end);
  } finally {
    closeSync(outFd);
  }
}

function main() {
  const { output, profile } = parseArgs(process.argv.slice(2));
  const workDir = mkdtempSync(join(tmpdir(), "multica-support-"));

  try {
    info("Checking Docker and self-host containers...");
    run("docker", ["version"]);

    const postgresId = composeContainerId("postgres");
    const backendId = composeContainerId("backend");
    const frontendId = composeContainerId("frontend");

    const postgresName = containerName(postgresId);
    const backendName = containerName(backendId);
    const frontendName = containerName(frontendId);
    const dbUser = containerEnv(postgresId, "POSTGRES_USER", "multica");
    const dbName = containerEnv(postgresId, "POSTGRES_DB", "multica");

    info(`Creating PostgreSQL dump from ${postgresName} (${dbName})...`);
    const dumpPath = join(workDir, "multica-db.dump");
    runBinaryToFile("docker", [
      "exec",
      postgresId,
      "pg_dump",
      "-U",
      dbUser,
      "-d",
      dbName,
      "-Fc",
      "--no-owner",
      "--no-privileges",
    ], dumpPath);

    info("Collecting container logs...");
    const logs = [
      [backendId, "backend.log"],
      [frontendId, "frontend.log"],
      [postgresId, "postgres.log"],
    ];
    for (const [containerId, fileName] of logs) {
      runToFile("docker", ["logs", "--timestamps", "--tail", LOG_TAIL_LINES, containerId], join(workDir, fileName));
    }

    info(`Collecting daemon diagnostics using profile '${profile}'...`);
    writeCommandResult(join(workDir, "daemon-status.txt"), "multica", ["daemon", "status", "--profile", profile]);
    writeCommandResult(join(workDir, "daemon-runtimes.txt"), "multica", ["daemon", "probe-runtimes", "--profile", profile]);

    info("Collecting Docker and Git metadata...");
    runToFile("docker", [...composeArgs(), "ps"], join(workDir, "docker-compose-ps.txt"));
    writeCommandResult(join(workDir, "docker-version.txt"), "docker", ["version"]);
    writeCommandResult(join(workDir, "git-status.txt"), "git", ["status", "--short", "--branch"]);

    const gitBranch = run("git", ["branch", "--show-current"], { allowFailure: true }).stdout.trim() || null;
    const gitCommit = run("git", ["rev-parse", "HEAD"], { allowFailure: true }).stdout.trim() || null;
    const cliVersion = run("multica", ["--version"], { allowFailure: true });

    writeFileSync(join(workDir, "metadata.json"), JSON.stringify({
      collected_at: new Date().toISOString(),
      git_branch: gitBranch,
      git_commit: gitCommit,
      multica_cli_version: cliVersion.ok ? cliVersion.stdout.trim() : null,
      daemon_profile: profile,
      log_tail_lines: Number(LOG_TAIL_LINES),
      postgres: { container: postgresName, database: dbName, user: dbUser },
      backend: { container: backendName },
      frontend: { container: frontendName },
      warning: "This bundle contains a database dump and application logs. Review it before sharing because it may contain sensitive project or user data.",
    }, null, 2));

    const files = [
      "multica-db.dump",
      "backend.log",
      "frontend.log",
      "postgres.log",
      "daemon-status.txt",
      "daemon-runtimes.txt",
      "docker-compose-ps.txt",
      "docker-version.txt",
      "git-status.txt",
      "metadata.json",
    ].map((name) => ({ name, path: join(workDir, name) }));

    info(`Creating ${basename(output)}...`);
    createZip(output, files);

    const sizeMb = (statSync(output).size / (1024 * 1024)).toFixed(1);
    console.log(`\nSupport bundle created:\n${output}\nSize: ${sizeMb} MiB`);
    console.log("\nWarning: the ZIP contains a full database dump and logs. Review it before sharing if the environment contains sensitive data.");
  } finally {
    rmSync(workDir, { recursive: true, force: true });
  }
}

try {
  main();
} catch (error) {
  fail(error instanceof Error ? error.message : String(error));
}
