// Test-only terminal renderer. Input and screen data stay in this local process.
import { createRequire } from "node:module";
import { createInterface } from "node:readline";
const require = createRequire(import.meta.url);
const { Terminal } = require("../../web/tests/vendor/xterm-headless.cjs");
function snapshot(term) {
  const buffer = term.buffer.active;
  const lines = Array.from({ length: term.rows }, (_, i) => buffer.getLine(buffer.viewportY + i)?.translateToString(true) ?? "");
  const cells = Array.from({ length: term.rows }, (_, y) => Array.from({ length: term.cols }, (_, x) => {
    const cell = buffer.getLine(buffer.viewportY + y)?.getCell(x);
    if (!cell) return [];
    const flags = ["isBold", "isDim", "isItalic", "isUnderline", "isBlink", "isInverse", "isInvisible", "isStrikethrough", "isOverline"].reduce((bits, method, i) => bits | (Number(Boolean(cell[method]?.())) << i), 0);
    return [cell.getFgColorMode(), cell.getFgColor(), cell.getBgColorMode(), cell.getBgColor(), flags, cell.getWidth()];
  }));
  // The pinned headless version has no public cursor-visibility accessor.
  return { lines, cells, cursorHidden: term._core.coreService.isCursorHidden, cursorX: buffer.cursorX, cursorY: buffer.cursorY, modes: term.modes, type: buffer.type };
}
async function apply(term, chunk) {
  if (chunk.cols) term.resize(chunk.cols, chunk.rows);
  if (chunk.data) await new Promise(resolve => term.write(Buffer.from(chunk.data, "base64"), resolve));
}
if (process.argv.includes("--stream")) {
  const term = new Terminal({ cols: 80, rows: 24, allowProposedApi: true });
  term.onData(reply => process.stdout.write(JSON.stringify({ reply: Buffer.from(reply).toString("base64") }) + "\n"));
  for await (const line of createInterface({ input: process.stdin })) {
    if (line.length > 8 * 1024 * 1024) throw new Error("screen input too large");
    const request = JSON.parse(line);
    await apply(term, request.chunk ?? {});
    process.stdout.write(JSON.stringify({ id: request.id, ...(request.snapshot ? { view: snapshot(term) } : {}) }) + "\n");
  }
  term.dispose();
} else {
  let input = "";
  for await (const chunk of process.stdin) {
    input += chunk;
    if (input.length > 8 * 1024 * 1024) throw new Error("screen input too large");
  }
  const request = JSON.parse(input);
  const term = new Terminal({ cols: request.cols, rows: request.rows, allowProposedApi: true });
  for (const chunk of request.chunks) await apply(term, chunk);
  process.stdout.write(JSON.stringify(snapshot(term)));
  term.dispose();
}
