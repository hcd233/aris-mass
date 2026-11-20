const NEWLINE_BYTE = 0x0a;
const TAIL_PREFIX_BYTES = 64;
const SIZE_ERROR = /^# dataset export: session (\d+) exceeds size limit$/;
const NDJSON_CONTENT_TYPE = "application/x-ndjson";

export const DATASET_EXPORT_MEMORY_LIMIT = 64 * 1024 * 1024;

type DownloadFailure =
  | { reason: "incomplete"; lines: number; total: number }
  | { reason: "session_too_large"; sessionId: string }
  | { reason: "memory_limit" };

export class DatasetDownloadError extends Error {
  constructor(readonly failure: DownloadFailure) {
    super(failure.reason);
  }
}

interface DatasetFileWriter {
  write(chunk: Uint8Array): Promise<void>;
  close(): Promise<void>;
  abort(): Promise<void>;
}

type ReportProgress = (lines: number, total: number) => void;

export async function openDatasetFile(file: string): Promise<DatasetFileWriter | null> {
  const browser = window as Window & {
    showSaveFilePicker?: (options: { suggestedName: string }) => Promise<FileSystemFileHandle>;
  };
  if (typeof browser.showSaveFilePicker !== "function") return null;

  const handle = await browser.showSaveFilePicker({ suggestedName: file });
  if (typeof handle.createWritable !== "function") return null;
  const stream = await handle.createWritable();
  return {
    write: (chunk) => stream.write(chunk as Uint8Array<ArrayBuffer>),
    close: () => stream.close(),
    abort: () => stream.abort(),
  };
}

export async function writeDatasetDownload(
  body: ReadableStream<Uint8Array>,
  total: number,
  file: DatasetFileWriter,
  onProgress: ReportProgress,
  signal: AbortSignal,
): Promise<number> {
  try {
    const bytes = await readDatasetDownload(
      body,
      total,
      (chunk) => file.write(chunk),
      onProgress,
      signal,
    );
    signal.throwIfAborted();
    await file.close();
    return bytes;
  } catch (err) {
    await file.abort().catch(() => {});
    throw err;
  }
}

export async function collectDatasetDownload(
  body: ReadableStream<Uint8Array>,
  total: number,
  onProgress: ReportProgress,
  signal: AbortSignal,
  limit = DATASET_EXPORT_MEMORY_LIMIT,
): Promise<Blob> {
  let buffer = new Uint8Array(0);
  let bytes = 0;
  await readDatasetDownload(
    body,
    total,
    (chunk) => {
      const next = bytes + chunk.byteLength;
      if (next > limit) throw new DatasetDownloadError({ reason: "memory_limit" });
      if (next > buffer.length) {
        const expanded = new Uint8Array(Math.min(limit, Math.max(next, buffer.length * 2)));
        expanded.set(buffer);
        buffer = expanded;
      }
      buffer.set(chunk, bytes);
      bytes = next;
    },
    onProgress,
    signal,
  );
  return new Blob([buffer.subarray(0, bytes)], { type: NDJSON_CONTENT_TYPE });
}

async function readDatasetDownload(
  body: ReadableStream<Uint8Array>,
  total: number,
  write: (chunk: Uint8Array) => void | Promise<void>,
  onProgress: ReportProgress,
  signal: AbortSignal,
): Promise<number> {
  const reader = body.getReader();
  const cancel = () => void reader.cancel(signal.reason).catch(() => {});
  signal.addEventListener("abort", cancel);

  let lines = 0;
  let bytes = 0;
  let tailLength = 0;
  const tailPrefix = new Uint8Array(TAIL_PREFIX_BYTES);
  try {
    for (;;) {
      signal.throwIfAborted();
      const { done, value } = await reader.read();
      signal.throwIfAborted();
      if (done) break;

      let start = 0;
      for (
        let end = value.indexOf(NEWLINE_BYTE);
        end !== -1;
        end = value.indexOf(NEWLINE_BYTE, start)
      ) {
        lines++;
        start = end + 1;
        tailLength = 0;
      }
      const prefixLength = Math.min(value.byteLength - start, tailPrefix.length - tailLength);
      tailPrefix.set(value.subarray(start, start + prefixLength), tailLength);
      tailLength += prefixLength;

      await write(value);
      bytes += value.byteLength;
      onProgress(lines, total);
    }

    if (tailLength > 0) {
      const match = SIZE_ERROR.exec(new TextDecoder().decode(tailPrefix.subarray(0, tailLength)));
      if (match)
        throw new DatasetDownloadError({ reason: "session_too_large", sessionId: match[1] });
    }
    if (!Number.isSafeInteger(total) || total <= 0 || lines !== total || tailLength > 0) {
      throw new DatasetDownloadError({ reason: "incomplete", lines, total });
    }
    return bytes;
  } catch (err) {
    await reader.cancel(err).catch(() => {});
    throw err;
  } finally {
    signal.removeEventListener("abort", cancel);
    reader.releaseLock();
  }
}
