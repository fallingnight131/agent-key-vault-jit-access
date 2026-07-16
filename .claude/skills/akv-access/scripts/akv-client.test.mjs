import assert from "node:assert/strict";
import test from "node:test";

import {
  AKVClient,
  CONTROL_ORIGIN,
  EXECUTION_ORIGIN,
  classifyExecutionResult,
  validateArguments,
} from "./akv-client.mjs";

function recordingFetch(status = 204, responseBody = "") {
  const calls = [];
  const fetch = async (url, options) => {
    calls.push({ url, options });
    return new Response(status === 204 ? null : responseBody, { status });
  };
  return { calls, fetch };
}

test("heartbeat always sends an empty JSON object", async () => {
  const recorder = recordingFetch();
  const client = new AKVClient("redacted", recorder.fetch);
  const result = await client.heartbeat("task-1");

  assert.equal(result.status, 204);
  assert.equal(recorder.calls.length, 1);
  assert.equal(recorder.calls[0].url, `${CONTROL_ORIGIN}/v1/agent/tasks/task-1/heartbeat`);
  assert.equal(recorder.calls[0].options.method, "POST");
  assert.equal(recorder.calls[0].options.headers["Content-Type"], "application/json");
  assert.equal(recorder.calls[0].options.body, "{}");
  assert.equal(recorder.calls[0].options.redirect, "manual");
});

test("execute sends only request_id and task_id with exact names", async () => {
  const recorder = recordingFetch(400, JSON.stringify({ error: "INVALID_REQUEST" }));
  const client = new AKVClient("redacted", recorder.fetch);
  await client.execute("task-1", "request-1");

  assert.equal(recorder.calls.length, 1);
  assert.equal(recorder.calls[0].url, `${EXECUTION_ORIGIN}/v1/execute`);
  assert.deepEqual(JSON.parse(recorder.calls[0].options.body), {
    request_id: "request-1",
    task_id: "task-1",
  });
});

test("arguments are checked against the discovered schema", () => {
  const schema = {
    type: "object",
    properties: { project_id: { type: "string", maxLength: 20 } },
    required: ["project_id"],
    additionalProperties: false,
  };

  assert.doesNotThrow(() => validateArguments(schema, { project_id: "12747" }));
  assert.throws(() => validateArguments(schema, { project_id: "12747", credential_id: "redacted" }), /undeclared/);
  assert.throws(() => validateArguments(schema, { project_id: 12747 }), /type/);
});

test("AKV 200 with target 502 is a failed business outcome", () => {
  const classification = classifyExecutionResult({
    status: 200,
    data: {
      operation_kind: "HTTP",
      result: { StatusCode: 502, Body: "dXBzdHJlYW0gdW5hdmFpbGFibGU=" },
    },
  });

  assert.equal(classification.operationKind, "HTTP");
  assert.equal(classification.targetStatus, 502);
  assert.equal(classification.succeeded, false);
  assert.deepEqual(classification.publicResult, {
    StatusCode: 502,
    Body: "dXBzdHJlYW0gdW5hdmFpbGFibGU=",
  });
});

test("malformed HTTP result defaults to failed business outcome", () => {
  const classification = classifyExecutionResult({
    status: 200,
    data: { operation_kind: "HTTP", result: {} },
  });

  assert.equal(classification.targetStatus, null);
  assert.equal(classification.succeeded, false);
});
