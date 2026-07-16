#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { pathToFileURL } from "node:url";

export const CONTROL_ORIGIN = "http://127.0.0.1:8080";
export const EXECUTION_ORIGIN = "http://127.0.0.1:8081";

const CONTROL_PATHS = [
  /^\/v1\/agent\/targets$/,
  /^\/v1\/agent\/targets\/[A-Za-z0-9-]+\/operations$/,
  /^\/v1\/agent\/tasks$/,
  /^\/v1\/agent\/tasks\/[A-Za-z0-9-]+\/(heartbeat|end)$/,
  /^\/v1\/agent\/authorizations$/,
  /^\/v1\/agent\/authorizations\/[A-Za-z0-9-]+$/,
  /^\/v1\/agent\/authorizations\/[A-Za-z0-9-]+\/revoke$/,
];

function fail(message) {
  throw new Error(message);
}

function parseJSON(value, label) {
  try {
    return JSON.parse(value);
  } catch {
    fail(`${label} must be valid JSON`);
  }
}

export function parseOptions(argv) {
  const [command, ...rest] = argv;
  const options = {};
  for (let index = 0; index < rest.length; index += 2) {
    const key = rest[index];
    const value = rest[index + 1];
    if (!key?.startsWith("--") || value === undefined) fail("options must be --name value pairs");
    options[key.slice(2)] = value;
  }
  return { command, options };
}

function required(options, name) {
  const value = options[name]?.trim();
  if (!value) fail(`missing --${name}`);
  return value;
}

export function validateArguments(schema, argumentsValue) {
  if (!schema || schema.type !== "object" || !schema.properties || typeof schema.properties !== "object") {
    fail("unsupported arguments schema");
  }
  if (!argumentsValue || Array.isArray(argumentsValue) || typeof argumentsValue !== "object") {
    fail("arguments must be a JSON object");
  }
  const properties = schema.properties;
  for (const key of Object.keys(argumentsValue)) {
    if (!(key in properties)) fail(`arguments contain undeclared property: ${key}`);
  }
  for (const key of schema.required || []) {
    if (!(key in argumentsValue)) fail(`arguments are missing required property: ${key}`);
  }
  for (const [key, value] of Object.entries(argumentsValue)) {
    const property = properties[key];
    const validType =
      (property.type === "string" && typeof value === "string") ||
      (property.type === "integer" && Number.isInteger(value)) ||
      (property.type === "number" && typeof value === "number" && Number.isFinite(value)) ||
      (property.type === "boolean" && typeof value === "boolean");
    if (!validType) fail(`argument type does not match schema: ${key}`);
    if (property.maxLength !== undefined && [...value].length > property.maxLength) fail(`argument exceeds maxLength: ${key}`);
    if (property.enum && !property.enum.includes(value)) fail(`argument is not in enum: ${key}`);
    if (property.minimum !== undefined && value < property.minimum) fail(`argument is below minimum: ${key}`);
    if (property.maximum !== undefined && value > property.maximum) fail(`argument is above maximum: ${key}`);
  }
}

export function loadToken(projectRoot = process.cwd()) {
  const tokenPath = path.join(projectRoot, ".agent-token");
  const stat = fs.lstatSync(tokenPath);
  if (!stat.isFile() || stat.isSymbolicLink()) fail(".agent-token must be a regular file, not a symlink");
  if (process.platform !== "win32" && (stat.mode & 0o777) !== 0o600) fail(".agent-token permissions must be 0600");

  const relative = ".agent-token";
  const ignored = spawnSync("git", ["check-ignore", "-q", "--", relative], { cwd: projectRoot, stdio: "ignore" });
  if (ignored.status !== 0) fail(".agent-token must be ignored by Git");
  const tracked = spawnSync("git", ["ls-files", "--error-unmatch", "--", relative], { cwd: projectRoot, stdio: "ignore" });
  if (tracked.status === 0) fail(".agent-token must not be tracked by Git");

  const token = fs.readFileSync(tokenPath, "utf8").trim();
  if (!token || /\s/.test(token)) fail(".agent-token must contain one non-empty token");
  return token;
}

function allowedPath(origin, requestPath) {
  if (origin === CONTROL_ORIGIN) return CONTROL_PATHS.some((pattern) => pattern.test(requestPath));
  return origin === EXECUTION_ORIGIN && requestPath === "/v1/execute";
}

export class AKVClient {
  constructor(token, fetchImplementation = globalThis.fetch) {
    if (!token || typeof fetchImplementation !== "function") fail("client configuration is invalid");
    this.token = token;
    this.fetch = fetchImplementation;
  }

  async call(origin, requestPath, { method = "GET", json, timeoutMs = 5000 } = {}) {
    if (!allowedPath(origin, requestPath)) fail("refusing unapproved AKV origin or path");
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const headers = { Authorization: `Bearer ${this.token}` };
      if (json !== undefined) headers["Content-Type"] = "application/json";
      const response = await this.fetch(origin + requestPath, {
        method,
        headers,
        body: json === undefined ? undefined : JSON.stringify(json),
        redirect: "manual",
        signal: controller.signal,
      });
      if (response.status >= 300 && response.status < 400) fail("AKV redirect rejected");
      const raw = await response.text();
      let data = null;
      if (raw) {
        try {
          data = JSON.parse(raw);
        } catch {
          data = { raw };
        }
      }
      return { status: response.status, data };
    } finally {
      clearTimeout(timeout);
    }
  }

  heartbeat(taskId) {
    return this.call(CONTROL_ORIGIN, `/v1/agent/tasks/${taskId}/heartbeat`, { method: "POST", json: {} });
  }

  execute(taskId, requestId) {
    return this.call(EXECUTION_ORIGIN, "/v1/execute", {
      method: "POST",
      json: { request_id: requestId, task_id: taskId },
      timeoutMs: 30000,
    });
  }
}

function expectStatus(result, expected, label) {
  if (result.status !== expected) fail(`${label} HTTP ${result.status}: ${JSON.stringify(result.data)}`);
  return result.data;
}

async function endTask(client, taskId, outcome) {
  const result = await client.call(CONTROL_ORIGIN, `/v1/agent/tasks/${taskId}/end`, {
    method: "POST",
    json: { outcome },
  });
  expectStatus(result, 204, "end task");
}

async function discoverFlow(client, options) {
  const targets = expectStatus(await client.call(CONTROL_ORIGIN, "/v1/agent/targets"), 200, "discover targets");
  const targetName = options.target?.trim();
  if (!targetName) {
    console.log(JSON.stringify({ phase: "discovered", targets }));
    return;
  }
  const matches = targets.filter((target) => target.name === targetName);
  if (matches.length !== 1) fail(`expected one target named ${targetName}, found ${matches.length}`);
  const target = matches[0];
  const discovered = expectStatus(await client.call(CONTROL_ORIGIN, target.operations_url), 200, "discover operations");
  console.log(JSON.stringify({ phase: "discovered", target, operations: discovered.operations }));
}

async function requestFlow(client, options) {
  const targetName = required(options, "target");
  const operationKey = required(options, "operation");
  const argumentsValue = parseJSON(required(options, "arguments"), "--arguments");
  const reason = required(options, "reason");

  const targets = expectStatus(await client.call(CONTROL_ORIGIN, "/v1/agent/targets"), 200, "discover targets");
  const matches = targets.filter((target) => target.name === targetName);
  if (matches.length !== 1) fail(`expected one target named ${targetName}, found ${matches.length}`);
  const target = matches[0];

  const discovered = expectStatus(await client.call(CONTROL_ORIGIN, target.operations_url), 200, "discover operations");
  const operations = discovered.operations.filter((operation) => operation.key === operationKey);
  if (operations.length !== 1) fail(`expected one operation keyed ${operationKey}, found ${operations.length}`);
  const operation = operations[0];
  validateArguments(operation.arguments_schema, argumentsValue);

  let taskId = "";
  let requestId = "";
  let heartbeatTimer;
  let heartbeatBusy = false;
  let stopping = false;

  const stop = async (outcome, exitCode) => {
    if (stopping) return;
    stopping = true;
    if (heartbeatTimer) clearInterval(heartbeatTimer);
    if (taskId) {
      try {
        await endTask(client, taskId, outcome);
      } catch {
        // The task may already have been ended by the execution process.
      }
    }
    process.exit(exitCode);
  };

  try {
    const task = expectStatus(
      await client.call(CONTROL_ORIGIN, "/v1/agent/tasks", { method: "POST", json: {} }),
      201,
      "begin task",
    );
    taskId = task.task_id;
    expectStatus(await client.heartbeat(taskId), 204, "initial heartbeat");

    heartbeatTimer = setInterval(async () => {
      if (heartbeatBusy || stopping) return;
      heartbeatBusy = true;
      try {
        const heartbeat = await client.heartbeat(taskId);
        if (heartbeat.status !== 204) {
          clearInterval(heartbeatTimer);
          console.error(JSON.stringify({ phase: "heartbeat_stopped", status: heartbeat.status, error: heartbeat.data?.error || null }));
          void stop("FAILED", 1);
        }
      } catch (error) {
        clearInterval(heartbeatTimer);
        console.error(JSON.stringify({ phase: "heartbeat_failed", error: error.message }));
        void stop("FAILED", 1);
      } finally {
        heartbeatBusy = false;
      }
    }, 15000);

    const authorization = expectStatus(
      await client.call(CONTROL_ORIGIN, "/v1/agent/authorizations", {
        method: "POST",
        json: {
          task_id: taskId,
          target_id: target.id,
          operation_id: operation.operation_id,
          version: operation.version,
          arguments: argumentsValue,
          reason,
        },
      }),
      201,
      "submit authorization",
    );
    requestId = authorization.request_id;
    console.log(JSON.stringify({
      phase: "waiting_for_approval",
      task_id: taskId,
      request_id: requestId,
      target: target.name,
      operation: operation.key,
      version: operation.version,
      arguments: argumentsValue,
      risk_level: operation.risk_level,
      approval_deadline: authorization.approval_deadline,
      heartbeat: "204 verified",
    }));

    process.once("SIGINT", () => void stop("CANCELLED", 130));
    process.once("SIGTERM", () => void stop("CANCELLED", 143));
    await new Promise(() => {});
  } catch (error) {
    if (heartbeatTimer) clearInterval(heartbeatTimer);
    if (taskId) {
      try {
        await endTask(client, taskId, "FAILED");
      } catch {
        // Report the original failure without hiding it behind cleanup failure.
      }
    }
    throw error;
  }
}

async function statusFlow(client, options) {
  const taskId = required(options, "task-id");
  const requestId = required(options, "request-id");
  const status = expectStatus(
    await client.call(CONTROL_ORIGIN, `/v1/agent/authorizations/${requestId}`),
    200,
    "authorization status",
  );
  console.log(JSON.stringify({ phase: "authorization_status", task_id: taskId, ...status }));
}

async function executeFlow(client, options) {
  const taskId = required(options, "task-id");
  const requestId = required(options, "request-id");
  const before = expectStatus(
    await client.call(CONTROL_ORIGIN, `/v1/agent/authorizations/${requestId}`),
    200,
    "authorization status",
  );
  if (before.request_status !== "APPROVED" || before.grant_status !== "APPROVED") {
    fail(`execution denied by status: request=${before.request_status} grant=${before.grant_status || "NONE"}`);
  }
  if (!before.grant_expires_at || Date.parse(before.grant_expires_at) <= Date.now()) fail("approved grant is expired");

  let execution;
  try {
    execution = await client.execute(taskId, requestId);
  } catch (error) {
    try {
      await endTask(client, taskId, "FAILED");
    } catch {}
    fail(`execution result is uncertain; do not retry: ${error.message}`);
  }

  const afterResult = await client.call(CONTROL_ORIGIN, `/v1/agent/authorizations/${requestId}`);
  const after = afterResult.status === 200 ? afterResult.data : null;
  const targetStatus = execution.data?.operation_kind === "HTTP" ? execution.data?.result?.StatusCode : null;
  const succeeded = execution.status === 200 && (targetStatus === null || (targetStatus >= 200 && targetStatus < 300));
  const publicResult = execution.data?.operation_kind === "HTTP"
    ? { StatusCode: targetStatus, Body: execution.data?.result?.Body || null }
    : execution.data?.result || null;
  try {
    await endTask(client, taskId, succeeded ? "COMPLETED" : "FAILED");
  } catch (error) {
    console.error(JSON.stringify({ phase: "end_task_failed", error: error.message }));
  }
  console.log(JSON.stringify({
    phase: "executed",
    execution_http_status: execution.status,
    operation_kind: execution.data?.operation_kind || null,
    target_status_code: targetStatus,
    result: publicResult,
    error: execution.data?.error || null,
    grant_status: after?.grant_status || null,
    execution_status: after?.execution_status || null,
    reclaim_status: after?.reclaim_status || null,
  }));
  if (!succeeded) process.exitCode = 1;
}

async function cancelFlow(client, options) {
  const taskId = required(options, "task-id");
  const requestId = options["request-id"]?.trim();
  if (requestId) {
    await client.call(CONTROL_ORIGIN, `/v1/agent/authorizations/${requestId}/revoke`, { method: "POST", json: {} });
  }
  await endTask(client, taskId, "CANCELLED");
  console.log(JSON.stringify({ phase: "cancelled", task_id: taskId, request_id: requestId || null }));
}

export async function main(argv = process.argv.slice(2)) {
  const { command, options } = parseOptions(argv);
  if (!["discover", "request", "status", "execute", "cancel"].includes(command)) {
    fail("usage: akv-client.mjs discover|request|status|execute|cancel [--name value ...]");
  }
  const client = new AKVClient(loadToken());
  if (command === "discover") return discoverFlow(client, options);
  if (command === "request") return requestFlow(client, options);
  if (command === "status") return statusFlow(client, options);
  if (command === "execute") return executeFlow(client, options);
  return cancelFlow(client, options);
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    console.error(JSON.stringify({ phase: "failed", error: error.message }));
    process.exitCode = 1;
  });
}
