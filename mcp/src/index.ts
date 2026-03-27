#!/usr/bin/env node
// Copyright (C) 2026 by Posit Software, PBC
// Licensed under the MIT License. See LICENSE for details.

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { registerCodeSearchTool } from "./code-search.js";

const server = new McpServer({
  name: "code-index",
  version: "0.1.0",
});

registerCodeSearchTool(server);

const transport = new StdioServerTransport();
await server.connect(transport);

process.on("SIGINT", async () => {
  await server.close();
  process.exit(0);
});

process.on("SIGTERM", async () => {
  await server.close();
  process.exit(0);
});
