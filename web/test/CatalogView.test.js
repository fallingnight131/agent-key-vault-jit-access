import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CatalogView from '../src/views/CatalogView.vue'

function catalogFixture() {
  return {
    targets: [{
      id: 'target-1',
      name: '工单 API',
      description: '测试目标',
      connector_type: 'HTTP',
      connection_config: { base_url: 'https://target.example.test' },
      default_credential_id: 'credential-1',
      active: true,
    }],
    credentials: [{
      id: 'credential-1',
      target_id: 'target-1',
      alias: 'default',
      credential_type: 'API_KEY',
      active: true,
      secret_values: { api_key: 'do-not-render-this-secret' },
      vault_path: 'kv/data/do-not-render-this-path',
    }],
    operation_sets: [{
      id: 'set-1',
      name: '工单安全操作',
      description: '只允许预先定义的工单操作',
      executor_type: 'HTTP',
      active: true,
    }],
    operations: [{
      id: 'operation-1',
      operation_set_id: 'set-1',
      key: 'ticket_read',
      current_version: 2,
      active: true,
    }],
    operation_versions: [
      {
        operation_id: 'operation-1',
        version: 1,
        name: '读取工单',
        description: '最初版本',
        operation_kind: 'HTTP',
        risk_level: 'LOW',
        arguments_schema: {
          type: 'object',
          properties: { ticket_id: { type: 'string' } },
          required: ['ticket_id'],
          additionalProperties: false,
        },
        execution_template: {
          kind: 'HTTP',
          http: { method: 'GET', path: '/tickets/{id}', path_arguments: { id: 'ticket_id' } },
        },
      },
      {
        operation_id: 'operation-1',
        version: 2,
        name: '读取工单详情',
        description: '当前对外参数',
        operation_kind: 'HTTP',
        risk_level: 'MEDIUM',
        arguments_schema: {
          type: 'object',
          properties: { ticket_id: { type: 'string', maxLength: 64 } },
          required: ['ticket_id'],
          additionalProperties: false,
        },
        execution_template: {
          kind: 'HTTP',
          http: { method: 'GET', path: '/tickets/{id}', path_arguments: { id: 'ticket_id' } },
        },
      },
    ],
    operation_bindings: [{
      target_id: 'target-1',
      operation_id: 'operation-1',
      version: 1,
      active: true,
    }],
  }
}

function catalogAPI() {
  return vi.fn(async (_path, options = {}) => (options.method ? {} : catalogFixture()))
}

function findButton(wrapper, label) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text() === label)
  expect(button, `missing button: ${label}`).toBeTruthy()
  return button
}

function dialogForm(wrapper) {
  return wrapper.get('dialog[open] form')
}

async function fillOperationDialog(wrapper, {
  key,
  name,
  description,
  risk = 'LOW',
  schema = '{"type":"object","properties":{},"required":[],"additionalProperties":false}',
  template = '{"kind":"HTTP","http":{"method":"GET","path":"/"}}',
}) {
  if (key !== undefined) await wrapper.get('input[name="operation_key"]').setValue(key)
  await wrapper.get('input[name="operation_name"]').setValue(name)
  await wrapper.get('textarea[name="operation_description"]').setValue(description)
  await wrapper.get('select[name="risk_level"]').setValue(risk)
  await wrapper.get('textarea[name="arguments_schema"]').setValue(schema)
  await wrapper.get('textarea[name="execution_template"]').setValue(template)
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('安全操作目录', () => {
  it('加载后显示当前版本、风险、公开 Schema 和精确目标绑定', async () => {
    const storageWrite = vi.spyOn(Storage.prototype, 'setItem')
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    expect(api).toHaveBeenCalledWith('/v1/web/catalog')
    expect(wrapper.text()).toContain('安全操作目录')
    expect(wrapper.text()).toContain('工单安全操作')
    expect(wrapper.text()).toContain('读取工单详情')
    expect(wrapper.text()).toContain('当前 v2')
    expect(wrapper.text()).toContain('MEDIUM')
    expect(wrapper.get('.operation-schema').text()).toContain('"ticket_id"')
    expect(wrapper.get('tbody tr').text()).toContain('工单 API (target-1)')
    expect(wrapper.get('tbody tr').text()).toContain('读取工单详情 (ticket_read)')
    expect(wrapper.get('tbody tr').text()).toContain('v1')
    expect(wrapper.text()).not.toContain('do-not-render-this-secret')
    expect(wrapper.text()).not.toContain('do-not-render-this-path')
    expect(storageWrite).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('创建操作集并发布 v1 操作时先在客户端解析 JSON', async () => {
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '新建操作集').trigger('click')
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain('新建安全操作集')
    await wrapper.get('input[name="operation_set_name"]').setValue('支付安全操作')
    await wrapper.get('textarea[name="operation_set_description"]').setValue('支付服务操作')
    await wrapper.get('select[name="executor_type"]').setValue('HTTP')
    await dialogForm(wrapper).trigger('submit')
    await flushPromises()
    const setCall = api.mock.calls.find(([path]) => path === '/v1/web/operation-sets')
    expect(setCall[1].method).toBe('POST')
    expect(JSON.parse(setCall[1].body)).toEqual({
      name: '支付安全操作',
      description: '支付服务操作',
      executor_type: 'HTTP',
    })

    await findButton(wrapper, '发布 v1 操作').trigger('click')
    await flushPromises()
    await fillOperationDialog(wrapper, {
      key: 'ticket_close',
      name: '关闭工单',
      description: '关闭指定工单',
      risk: 'HIGH',
      template: '{"kind":"HTTP","http":{"method":"POST","path":"/tickets/close"}}',
    })
    await dialogForm(wrapper).trigger('submit')
    await flushPromises()

    const operationCall = api.mock.calls.find(([path]) => path === '/v1/web/operation-sets/set-1/operations')
    expect(operationCall[1].method).toBe('POST')
    expect(JSON.parse(operationCall[1].body)).toEqual({
      key: 'ticket_close',
      name: '关闭工单',
      description: '关闭指定工单',
      risk_level: 'HIGH',
      arguments_schema: { type: 'object', properties: {}, required: [], additionalProperties: false },
      execution_template: { kind: 'HTTP', http: { method: 'POST', path: '/tickets/close' } },
    })
    expect(operationCall[1].body).not.toContain('do-not-render-this-secret')
    wrapper.unmount()
  })

  it('JSON 输入错误时不提交，保留草稿后可重试', async () => {
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '发布 v1 操作').trigger('click')
    await flushPromises()
    await fillOperationDialog(wrapper, {
      key: 'ticket_close',
      name: '关闭工单',
      description: '关闭指定工单',
      risk: 'HIGH',
      schema: '{',
      template: '{"kind":"HTTP","http":{"method":"POST","path":"/tickets/close"}}',
    })
    await dialogForm(wrapper).trigger('submit')
    expect(wrapper.get('[role="alert"]').text()).toContain('必须是有效的 JSON 对象')
    expect(api.mock.calls.filter(([path]) => path.includes('/operations')).length).toBe(0)
    expect(wrapper.get('input[name="operation_key"]').element.value).toBe('ticket_close')

    await wrapper.get('textarea[name="arguments_schema"]').setValue(
      '{"type":"object","properties":{},"required":[],"additionalProperties":false}',
    )
    await dialogForm(wrapper).trigger('submit')
    await flushPromises()

    expect(api.mock.calls.some(([path]) => path === '/v1/web/operation-sets/set-1/operations')).toBe(true)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('发布新版本时不重新发送操作键', async () => {
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '发布新版本').trigger('click')
    await flushPromises()
    expect(wrapper.find('input[name="operation_key"]').exists()).toBe(false)
    await fillOperationDialog(wrapper, {
      name: '读取工单 v3',
      description: '缩小参数范围',
      risk: 'LOW',
      template: '{"kind":"HTTP","http":{"method":"GET","path":"/tickets"}}',
    })
    await dialogForm(wrapper).trigger('submit')
    await flushPromises()

    const publishCall = api.mock.calls.find(([path]) => path === '/v1/web/operations/operation-1/versions')
    const body = JSON.parse(publishCall[1].body)
    expect(publishCall[1].method).toBe('POST')
    expect(body.name).toBe('读取工单 v3')
    expect(body).not.toHaveProperty('key')
    expect(body.arguments_schema).toEqual({ type: 'object', properties: {}, required: [], additionalProperties: false })
    wrapper.unmount()
  })

  it('把选定的精确版本绑定到具体目标', async () => {
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '绑定到目标').trigger('click')
    await flushPromises()
    expect(wrapper.get('select[name="binding_target"]').element.value).toBe('target-1')
    await wrapper.get('select[name="binding_version"]').setValue('1')
    await dialogForm(wrapper).trigger('submit')
    await flushPromises()

    const bindingCall = api.mock.calls.find(([path]) => path === '/v1/web/targets/target-1/operations/operation-1')
    expect(bindingCall[1].method).toBe('PUT')
    expect(JSON.parse(bindingCall[1].body)).toEqual({ version: 1, active: true })
    wrapper.unmount()
  })

  it('取消或提交失败后清空 HTTP 目标弹窗中的敏感输入', async () => {
    const api = vi.fn(async (_path, options = {}) => {
      if (!options.method) return catalogFixture()
      throw new Error('目标创建失败')
    })
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '新建 HTTP 目标').trigger('click')
    await flushPromises()
    await wrapper.get('input[name="api_key"]').setValue('temporary-api-key')
    await findButton(wrapper, '取消').trigger('click')
    await flushPromises()

    expect(api.mock.calls.filter(([, options = {}]) => options.method === 'POST')).toHaveLength(0)
    expect(wrapper.find('dialog[open]').exists()).toBe(false)

    await findButton(wrapper, '新建 HTTP 目标').trigger('click')
    await flushPromises()
    expect(wrapper.get('input[name="api_key"]').element.value).toBe('')

    await wrapper.get('input[name="target_name"]').setValue('演示 API')
    await wrapper.get('input[name="base_url"]').setValue('https://target.example.test')
    await wrapper.get('input[name="api_key"]').setValue('one-shot-api-key')
    await dialogForm(wrapper).trigger('submit')
    await flushPromises()

    expect(api.mock.calls.filter(([, options = {}]) => options.method === 'POST')).toHaveLength(1)
    expect(wrapper.get('dialog[open] [role="alert"]').text()).toBe('目标创建失败')
    expect(wrapper.get('input[name="api_key"]').element.value).toBe('')
    wrapper.unmount()
  })

  it('更新凭证弹窗的标题区明确显示别名和凭证类型', async () => {
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '更新凭证').trigger('click')
    await flushPromises()

    expect(wrapper.get('dialog[open] h2').text()).toContain('更新凭证')
    const description = wrapper.get('dialog[open] .modal-description').text()
    expect(description).toContain('default')
    expect(description).toContain('API_KEY')
    wrapper.unmount()
  })

  it('点击 HTTP 目标输入弹窗的背景不会关闭或丢失草稿', async () => {
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '新建 HTTP 目标').trigger('click')
    await flushPromises()
    await wrapper.get('input[name="target_name"]').setValue('未完成的演示目标')

    await wrapper.get('dialog[open]').trigger('click')
    await flushPromises()

    expect(wrapper.find('dialog[open]').exists()).toBe(true)
    expect(wrapper.get('input[name="target_name"]').element.value).toBe('未完成的演示目标')
    wrapper.unmount()
  })

  it('可分别停用操作集、操作和精确版本绑定', async () => {
    const api = catalogAPI()
    const wrapper = mount(CatalogView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '停用操作集').trigger('click')
    await flushPromises()
    await findButton(wrapper, '停用操作').trigger('click')
    await flushPromises()
    await findButton(wrapper, '停用绑定').trigger('click')
    await flushPromises()

    const setCall = api.mock.calls.find(([path]) => path === '/v1/web/operation-sets/set-1')
    const operationCall = api.mock.calls.find(([path]) => path === '/v1/web/operations/operation-1')
    const bindingCall = api.mock.calls.find(([path, options]) => (
      path === '/v1/web/targets/target-1/operations/operation-1' && JSON.parse(options.body).active === false
    ))
    expect(setCall[1]).toMatchObject({ method: 'PATCH' })
    expect(JSON.parse(setCall[1].body)).toEqual({ active: false })
    expect(operationCall[1]).toMatchObject({ method: 'PATCH' })
    expect(JSON.parse(operationCall[1].body)).toEqual({ active: false })
    expect(bindingCall[1]).toMatchObject({ method: 'PUT' })
    expect(JSON.parse(bindingCall[1].body)).toEqual({ version: 1, active: false })
    wrapper.unmount()
  })
})
