import { describe, expect, it, vi } from "vitest";

import { collectDatasetDownload, writeDatasetDownload } from "./dataset-download";

const encoder = new TextEncoder();

function body(...parts: string[]): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(controller) {
      for (const part of parts) controller.enqueue(encoder.encode(part));
      controller.close();
    },
  });
}

function writer() {
  return {
    write: vi.fn(async () => {}),
    close: vi.fn(async () => {}),
    abort: vi.fn(async () => {}),
  };
}

describe("dataset downloads", () => {
  it("writes each chunk and commits only after all lines arrive", async () => {
    const target = writer();
    const progress = vi.fn();
    const first = '{"conversations":[]}\n';
    const second = '{"conversations":[{"value":"猫"}]}';
    const full = first + second + "\n";
    const result = await writeDatasetDownload(
      body(first, second, "\n"),
      2,
      target,
      progress,
      new AbortController().signal,
    );
    expect(target.write.mock.calls).toEqual([
      [encoder.encode(first)],
      [encoder.encode(second)],
      [encoder.encode("\n")],
    ]);
    expect(progress.mock.calls).toEqual([
      [1, 2],
      [1, 2],
      [2, 2],
    ]);
    expect(target.close).toHaveBeenCalledTimes(1);
    expect(target.abort).not.toHaveBeenCalled();
    expect(result).toBe(encoder.encode(full).byteLength);
  });

  it("aborts the file on a split terminal size error and excludes it from progress", async () => {
    const target = writer();
    const progress = vi.fn();
    await expect(
      writeDatasetDownload(
        body('{"conversations":[]}\n# dataset export: ses', "sion 82 exceeds size limit"),
        2,
        target,
        progress,
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ failure: { reason: "session_too_large", sessionId: "82" } });
    expect(progress.mock.calls).toEqual([
      [1, 2],
      [1, 2],
    ]);
    expect(target.abort).toHaveBeenCalledTimes(1);
    expect(target.close).not.toHaveBeenCalled();
  });

  it("aborts when an incomplete response contains fewer complete lines", async () => {
    const target = writer();
    await expect(
      writeDatasetDownload(
        body('{"conversations":[]}\n{"conversations":['),
        2,
        target,
        vi.fn(),
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({ failure: { reason: "incomplete", lines: 1, total: 2 } });
    expect(target.abort).toHaveBeenCalledTimes(1);
    expect(target.close).not.toHaveBeenCalled();
  });

  it("cancels the response and aborts the file when the user cancels after a chunk", async () => {
    const controller = new AbortController();
    const target = writer();
    const cancel = vi.fn();
    const incoming = new ReadableStream<Uint8Array>({
      start(stream) {
        stream.enqueue(encoder.encode('{"conversations":[]}\n'));
      },
      cancel,
    });
    await expect(
      writeDatasetDownload(incoming, 2, target, () => controller.abort(), controller.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(target.abort).toHaveBeenCalledTimes(1);
    expect(target.close).not.toHaveBeenCalled();
  });

  it("cancels the network reader if a browser without file streaming exceeds its limit", async () => {
    const cancel = vi.fn();
    const incoming = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array(17));
      },
      cancel,
    });
    await expect(
      collectDatasetDownload(incoming, 1, vi.fn(), new AbortController().signal, 16),
    ).rejects.toMatchObject({ failure: { reason: "memory_limit" } });
    expect(cancel).toHaveBeenCalledTimes(1);
  });
});
