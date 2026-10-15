interface D1PreparedStatement {
  bind(...values: unknown[]): D1PreparedStatement
  first<T = Record<string, unknown>>(): Promise<T | null>
  all<T = Record<string, unknown>>(): Promise<{ results: T[] }>
  run(): Promise<{ meta: { changes: number } }>
}
interface D1Database {
  prepare(query: string): D1PreparedStatement
  batch(statements: D1PreparedStatement[]): Promise<unknown[]>
}
interface R2UploadedPart { partNumber: number; etag: string }
interface R2MultipartUpload {
  uploadId: string
  uploadPart(partNumber: number, value: Uint8Array | ReadableStream): Promise<R2UploadedPart>
  complete(parts: R2UploadedPart[]): Promise<unknown>
  abort(): Promise<void>
}
interface R2ObjectBody { body: ReadableStream; size: number }
interface R2Bucket {
  createMultipartUpload(key: string): Promise<R2MultipartUpload>
  resumeMultipartUpload(key: string, uploadId: string): R2MultipartUpload
  put(key: string, value: Uint8Array, options?: { httpMetadata?: { contentType?: string } }): Promise<unknown>
  get(key: string, options?: { range?: { offset: number; length: number } }): Promise<R2ObjectBody | null>
  head(key: string): Promise<{ size: number } | null>
  delete(key: string): Promise<void>
}
interface Fetcher { fetch(request: Request): Promise<Response> }
interface ScheduledEvent { scheduledTime: number }
