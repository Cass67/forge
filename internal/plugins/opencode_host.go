package plugins

import (
	"os"
	"path/filepath"
)

const OpenCodeHostFileName = "opencode-host.mjs"

func WriteOpenCodeHost(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(openCodeHostScript), 0o600)
}

const openCodeHostScript = `#!/usr/bin/env node
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import path from "node:path";
import fs from "node:fs";
import readline from "node:readline";
import child_process from "node:child_process";

const args = parseArgs(process.argv.slice(2));
const moduleSpec = args.module || process.env.FORGE_OPENCODE_PLUGIN_MODULE;
const installDir = args.installDir || process.env.FORGE_OPENCODE_PLUGIN_INSTALL_DIR || process.cwd();
let cwd = process.cwd();
let instance = null;
let tools = {};
let forgeTools = [];

if (!moduleSpec) {
  throw new Error("missing --module for OpenCode plugin host");
}

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

rl.on("line", async (line) => {
  if (!line.trim()) return;
  let req;
  try {
    req = JSON.parse(line);
  } catch (error) {
    write({ id: 0, error: { message: "invalid JSON request: " + errorMessage(error) } });
    return;
  }
  try {
    const result = await dispatch(req.method, req.params || {});
    write({ id: req.id, result });
  } catch (error) {
    write({ id: req.id, error: { message: errorMessage(error) } });
  }
});

async function dispatch(method, params) {
  switch (method) {
    case "initialize":
      cwd = params.cwd || process.cwd();
      if (Array.isArray(params.forge_tools)) forgeTools = params.forge_tools;
      await ensurePlugin(params.plugin_id || "opencode");
      return {
        tools: Object.entries(tools).map(([name, definition]) => ({
          name,
          description: typeof definition.description === "string" ? definition.description : "OpenCode plugin tool " + name,
          parameters: parametersFromArgs(definition.args || definition.parameters || {}),
        })),
        hooks: supportedHooks(instance),
      };
    case "tool_call":
      await ensurePlugin("opencode");
      return await callTool(params.name, params.arguments || {});
    case "hook":
      await ensurePlugin("opencode");
      return await callHook(params);
    default:
      throw new Error("unknown method: " + method);
  }
}

async function ensurePlugin(pluginID) {
  if (instance) return;
  const mod = await import(resolveModule(moduleSpec));
  const exported = mod.default || mod;
  const server = typeof exported === "function" ? exported : exported.server;
  if (typeof server !== "function") {
    throw new Error("OpenCode plugin module does not export a server function");
  }
  instance = await server(createPluginInput(pluginID), {});
  if (!instance || typeof instance !== "object") {
    throw new Error("OpenCode plugin server did not return a plugin object");
  }
  tools = instance.tool && typeof instance.tool === "object" ? instance.tool : {};
}

function resolveModule(spec) {
  if (spec.startsWith("file://")) return spec;
  if (path.isAbsolute(spec) || spec.startsWith(".")) {
    return pathToFileURL(path.resolve(cwd, spec)).href;
  }
  const require = createRequire(path.join(installDir, "forge-opencode-host.cjs"));
  return pathToFileURL(require.resolve(spec, { paths: [installDir, process.cwd()] })).href;
}

function createPluginInput(pluginID) {
  return {
    directory: cwd,
    project: cwd,
    worktree: cwd,
    serverUrl: "forge://opencode-compat",
    client: createCompatClient(),
    experimental_workspace: undefined,
    $: async () => {
      throw new Error("Unsupported OpenCode runtime API: shell helper $. Forge OpenCode compatibility currently supports simple plugin tools only.");
    },
    skills: [],
    pluginID,
  };
}

function createCompatClient() {
  const unsupported = (name) => async () => {
    throw new Error("Unsupported OpenCode client API: " + name + ". Forge OpenCode compatibility currently supports simple plugin tools only.");
  };
  return {
    _client: {
      getConfig: () => ({}),
      setConfig: () => {},
    },
    tui: {
      showToast: async () => {},
    },
    provider: {
      list: unsupported("client.provider.list"),
    },
    model: {
      list: unsupported("client.model.list"),
    },
    session: new Proxy({}, {
      get: (_target, prop) => unsupported("client.session." + String(prop)),
    }),
    agent: new Proxy({}, {
      get: (_target, prop) => unsupported("client.agent." + String(prop)),
    }),
    file: {
      list: async (params) => {
        const dir = params?.path ? path.resolve(cwd, params.path) : cwd;
        const entries = fs.readdirSync(dir, { withFileTypes: true });
        return entries.map((e) => ({
          name: e.name,
          type: e.isDirectory() ? "directory" : e.isFile() ? "file" : "other",
        }));
      },
      read: async (params) => {
        if (!params.path || String(params.path).trim() === "") {
          throw new Error("file.read requires a non-empty path");
        }
        const resolvedCwd = path.resolve(cwd);
        const filePath = path.resolve(cwd, params.path);
        const normalized = path.resolve(filePath);
        if (!normalized.startsWith(resolvedCwd + path.sep) && normalized !== resolvedCwd) {
          throw new Error("path traversal detected: " + params.path);
        }
        let content = fs.readFileSync(filePath, "utf-8");
        if (typeof params.offset === "number") {
          const lines = content.split("\n");
          const start = Math.max(0, params.offset);
          const end = typeof params.limit === "number" ? start + params.limit : lines.length;
          content = lines.slice(start, end).join("\n");
        }
        return { content, path: filePath };
      },
      status: async () => {
        try {
          const output = child_process.execSync("git status --porcelain", { cwd, encoding: "utf-8", maxBuffer: 1024 * 1024, stdio: ["pipe", "pipe", "ignore"] });
          return { changes: output.trim().split("\n").filter(Boolean) };
        } catch {
          return { changes: [], error: "not a git repository or git not available" };
        }
      },
    },
    find: {
      text: async (params) => {
        const pattern = params.pattern || "";
        if (!pattern) return { matches: [] };
        const dir = params.path ? path.resolve(cwd, params.path) : cwd;
        let output;
        try {
          const args = ["--no-heading", "--line-number", "-e", pattern];
          if (params.fileTypes) args.push("--type", params.fileTypes);
          output = child_process.execSync("rg", args.concat([dir]), { cwd, encoding: "utf-8", maxBuffer: 10 * 1024 * 1024, stdio: ["pipe", "pipe", "ignore"] });
        } catch {
          output = child_process.execSync("grep", ["-Irn", pattern, dir], { cwd, encoding: "utf-8", maxBuffer: 10 * 1024 * 1024, stdio: ["pipe", "pipe", "ignore"] });
        }
        const lines = output.trim().split("\n").filter(Boolean);
        return { matches: lines.map(parseRgLine) };
      },
      files: async (params) => {
        const dir = params?.path ? path.resolve(cwd, params.path) : cwd;
        let output;
        try {
          output = child_process.execSync("rg", ["--files", dir], { cwd, encoding: "utf-8", maxBuffer: 10 * 1024 * 1024, stdio: ["pipe", "pipe", "ignore"] });
        } catch {
          try {
            output = child_process.execSync("git", ["ls-files", "--cached", "--others", "--exclude-standard", "--full-name", dir], { cwd, encoding: "utf-8", maxBuffer: 10 * 1024 * 1024, stdio: ["pipe", "pipe", "ignore"] });
          } catch {
            output = child_process.execSync("find", [dir, "-type", "f"], { cwd, encoding: "utf-8", maxBuffer: 10 * 1024 * 1024 });
          }
        }
        return { files: output.trim().split("\n").filter(Boolean) };
      },
    },
    tool: {
      ids: async () => Object.keys(tools),
      list: async () => {
        const result = {};
        for (const name of Object.keys(tools)) {
          result[name] = {
            description: typeof tools[name].description === "string" ? tools[name].description : "OpenCode plugin tool " + name,
            parameters: parametersFromArgs(tools[name].args || tools[name].parameters || {}),
          };
        }
        for (const name of forgeTools) {
          if (!result[name]) {
            result[name] = { description: "forge tool " + name, parameters: [] };
          }
        }
        return result;
      },
    },
    app: {
      log: async (msg) => {
        process.stdout.write("__LOG__:" + String(msg) + "\n");
      },
    },
  };
}

function parseRgLine(line) {
  const lastColon = line.lastIndexOf(":");
  if (lastColon < 0) return { file: line, line: 1, content: "" };
  const content = line.substring(lastColon + 1);
  const beforeContent = line.substring(0, lastColon);
  const secondLastColon = beforeContent.lastIndexOf(":");
  if (secondLastColon < 0) return { file: beforeContent, line: 1, content };
  const file = line.substring(0, secondLastColon);
  const lineNum = parseInt(line.substring(secondLastColon + 1, lastColon), 10);
  return { file, line: lineNum || 1, content };
}

async function callTool(name, inputArgs) {
  const definition = tools[name];
  if (!definition || typeof definition.execute !== "function") {
    throw new Error("unknown OpenCode plugin tool: " + name);
  }
  const output = await definition.execute(inputArgs, {
    sessionID: "forge-session",
    callID: "forge-call",
    metadata: {},
  });
  return { content: stringifyToolOutput(output) };
}

async function callHook(params) {
  if (params.point === "before_tool" && typeof instance["tool.execute.before"] === "function") {
    const output = { args: params.args || {} };
    try {
      await instance["tool.execute.before"]({ tool: params.tool_name || "", sessionID: "forge-session", callID: "forge-call" }, output);
    } catch (error) {
      return { block: { message: errorMessage(error) } };
    }
    if (JSON.stringify(output.args || {}) !== JSON.stringify(params.args || {})) {
      return { note: { message: "OpenCode plugin modified tool arguments, but Forge cannot apply OpenCode argument mutations yet." } };
    }
    return {};
  }
  if (params.point === "after_tool" && typeof instance["tool.execute.after"] === "function") {
    const output = {
      title: params.tool_name || "tool",
      output: params.error || "",
      metadata: { forge_status: params.status || "ok" },
    };
    await instance["tool.execute.after"]({ tool: params.tool_name || "", sessionID: "forge-session", callID: "forge-call" }, output);
    return {};
  }
  if (params.point === "chat_message" && typeof instance["chat.message"] === "function") {
    await instance["chat.message"](params.event || {});
    return {};
  }
  if (params.point === "chat_params" && typeof instance["chat.params"] === "function") {
    await instance["chat.params"](params.event || {});
    return {};
  }
  if (params.point === "chat_headers" && typeof instance["chat.headers"] === "function") {
    await instance["chat.headers"](params.event || {});
    return {};
  }
  if (params.point === "permission_request" && typeof instance["permission.ask"] === "function") {
    const output = {};
    await instance["permission.ask"](params.event || {}, output);
    if (output.block) {
      return { block: { message: output.block.message || output.block || "Blocked by plugin" } };
    }
    return {};
  }
  if (params.point === "event" && typeof instance["event"] === "function") {
    await instance["event"](params.event || {});
    return {};
  }
  return {};
}

function supportedHooks(plugin) {
  const hooks = [];
  if (plugin && typeof plugin["tool.execute.before"] === "function") hooks.push("before_tool");
  if (plugin && typeof plugin["tool.execute.after"] === "function") hooks.push("after_tool");
  if (plugin && typeof plugin["chat.message"] === "function") hooks.push("chat_message");
  if (plugin && typeof plugin["chat.params"] === "function") hooks.push("chat_params");
  if (plugin && typeof plugin["chat.headers"] === "function") hooks.push("chat_headers");
  if (plugin && typeof plugin["permission.ask"] === "function") hooks.push("permission_request");
  if (plugin && typeof plugin["event"] === "function") hooks.push("event");
  if (plugin && typeof plugin["config"] === "function") hooks.push("prompt_context");
  if (plugin && typeof plugin["command.execute.before"] === "function") hooks.push("before_tool");
  return hooks;
}

function parametersFromArgs(args) {
  if (!args || typeof args !== "object") return [];
  return Object.entries(args).map(([name, schema]) => parameterFromSchema(name, schema));
}

function parameterFromSchema(name, schema) {
  return {
    name,
    type: parameterType(schema),
    description: schemaDescription(schema),
    required: !schemaIsOptional(schema),
  };
}

function parameterType(schema) {
  const unwrapped = unwrapSchema(schema);
  const kind = String(unwrapped?._def?.typeName || unwrapped?._def?.type || unwrapped?.type || "").toLowerCase();
  if (kind.includes("bool")) return "bool";
  if (kind.includes("number") || kind.includes("int")) return "int";
  return "string";
}

function schemaDescription(schema) {
  const unwrapped = unwrapSchema(schema);
  return String(schema?.description || schema?._def?.description || unwrapped?.description || unwrapped?._def?.description || "");
}

function schemaIsOptional(schema) {
  if (!schema || typeof schema !== "object") return false;
  try {
    if (typeof schema.isOptional === "function" && schema.isOptional()) return true;
  } catch {}
  const kind = String(schema._def?.typeName || schema._def?.type || "").toLowerCase();
  return kind.includes("optional") || kind.includes("default");
}

function unwrapSchema(schema) {
  let current = schema;
  for (let i = 0; i < 8; i++) {
    const kind = String(current?._def?.typeName || current?._def?.type || "").toLowerCase();
    const next = current?._def?.innerType || current?._def?.schema;
    if (!next || (!kind.includes("optional") && !kind.includes("default") && !kind.includes("nullable"))) break;
    current = next;
  }
  return current;
}

function stringifyToolOutput(output) {
  if (output == null) return "";
  if (typeof output === "string") return output;
  if (typeof output.output === "string") return output.output;
  if (typeof output.content === "string") return output.content;
  return JSON.stringify(output);
}

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i++) {
    const item = argv[i];
    switch (item) {
      case "--module":
        parsed.module = argv[++i];
        break;
      case "--install-dir":
        parsed.installDir = argv[++i];
        break;
    }
  }
  return parsed;
}

function write(payload) {
  process.stdout.write(JSON.stringify(payload) + "\n");
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}
`
