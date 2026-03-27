import { strict as assert } from "node:assert";

import plugin from "../index.js";

const infoLogs: string[] = [];

plugin.register({
  runtime: {
    logger: {},
  },
  logger: {
    info: (msg: string) => infoLogs.push(msg),
    warn: () => {},
    error: () => {},
    debug: () => {},
  },
  registerChannel: () => {},
  on: () => {},
} as any);

assert.equal(infoLogs.length, 1, "register() should fall back to api.logger when runtime.logger is unusable");
assert.match(infoLogs[0], /AstronClaw v.*registered as channel plugin/);

console.log("PASS test_logger_resolution");
