import { afterEach, describe, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('../../client', () => ({ apiClient: mocks }))
import { canStreamExport, exportCapture, getContent, listRecords, createTask } from '../requestCaptures'
afterEach(() => { vi.clearAllMocks(); Reflect.deleteProperty(window, 'showSaveFilePicker') })
describe('request capture API', () => {
  it('streams authenticated export to disk without a Blob', async () => {
    const writable = { abort: vi.fn() }, pipeTo = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(window, 'showSaveFilePicker', { configurable: true, value: vi.fn().mockResolvedValue({ createWritable: async () => writable }) })
    mocks.get.mockResolvedValue({ data: { pipeTo } })
    expect(canStreamExport()).toBe(true)
    await exportCapture('task', 'record')
    expect(mocks.get).toHaveBeenCalledWith('/admin/request-captures/task/requests/record/export', { adapter: 'fetch', responseType: 'stream', timeout: 0 })
    expect(pipeTo).toHaveBeenCalledWith(writable)
  })
  it('aborts incomplete files on a failed download', async () => {
    const abort = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(window, 'showSaveFilePicker', { configurable: true, value: vi.fn().mockResolvedValue({ createWritable: async () => ({ abort }) }) })
    mocks.get.mockRejectedValueOnce(new Error('connection lost'))
    await expect(exportCapture('task')).rejects.toThrow('connection lost')
    expect(abort).toHaveBeenCalledTimes(1)
  })
  it('sends pagination, exact request filters and bounded preview offsets', async () => {
    mocks.get.mockResolvedValue({ data: {} }); mocks.post.mockResolvedValue({ data: {} })
    await listRecords('task', 2, 'request-id', true)
    expect(mocks.get).toHaveBeenLastCalledWith('/admin/request-captures/task/requests', { params: { page: 2, page_size: 20, request_id: 'request-id', errors_only: true } })
    await getContent('task', 'record', 'a.txt', 262144)
    expect(mocks.get).toHaveBeenLastCalledWith('/admin/request-captures/task/requests/record/content/a.txt', { params: { offset: 262144 } })
    const body = { target_type: 'account' as const, target_id: 2, duration_minutes: 1, save_media: false }
    await createTask(body); expect(mocks.post).toHaveBeenCalledWith('/admin/request-captures', body)
  })
})
