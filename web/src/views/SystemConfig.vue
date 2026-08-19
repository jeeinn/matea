<template>
  <div class="system-config-page">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>系统配置</span>
          <el-button type="primary" :loading="saving" @click="saveAll">
            <el-icon><Check /></el-icon>
            保存全部
          </el-button>
        </div>
      </template>

      <el-alert
        v-if="setupHint"
        :title="setupHint"
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 16px"
      />

      <el-tabs v-model="activeTab">
        <!-- Tab 1: Gitea 连接 -->
        <el-tab-pane label="Gitea 连接" name="gitea">
          <el-form label-width="140px" class="config-form">
            <el-form-item label="Gitea 地址">
              <el-input v-model="form['gitea.url']" placeholder="http://localhost:3000" />
              <div class="form-tip">
                Gitea 服务的访问地址
                <el-tag v-if="sourceTag('gitea.url')" size="small" :type="sourceTag('gitea.url') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('gitea.url') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label="管理员 Token">
              <el-input v-model="form['gitea.admin_token']" type="password" show-password placeholder="Gitea 管理员 Token" />
              <div class="form-tip">
                用于自动创建 Agent 账号，需包含 <code>write:admin</code> 权限。<br>
                获取路径：登录管理员 → 头像 → 设置 → 应用 → 生成新令牌（勾选 admin / repository 相关写权限）<br>
                <strong>安全提示：已保存的 Token 以 <code>********</code> 掩码显示——保持掩码不变则沿用原值，输入新值即替换。</strong>
                <el-tag v-if="sourceTag('gitea.admin_token')" size="small" :type="sourceTag('gitea.admin_token') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('gitea.admin_token') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label="Webhook 密钥">
              <el-input v-model="form['gitea.webhook_secret']" type="password" show-password placeholder="留空则保存时自动生成" />
              <div class="form-tip">
                自拟一串密钥填到此处，并在 Gitea Webhook 的「密钥」里填<strong>相同值</strong>（两边一致即可，不是从 Gitea 导出的）；<strong>留空保存时系统会自动生成随机密钥</strong>。<br>
                全站（推荐）：站点管理 → Webhooks → 添加 Webhook（Gitea），目标 URL
                <code>http://&lt;matea-host&gt;:8080/webhook/gitea</code>，勾选 Issues / Issue 评论 / Pull Request / PR 评论。<br>
                也可只配组织级（组织设置 → Webhooks）或单个仓库（仓库设置 → Webhooks）。掩码规则同上方 Token。
                <el-tag v-if="sourceTag('gitea.webhook_secret')" size="small" :type="sourceTag('gitea.webhook_secret') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('gitea.webhook_secret') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label=" ">
              <el-button :loading="testingGitea" @click="testGitea">测试 Gitea 连接</el-button>
              <span v-if="giteaTestMessage" :class="['test-result', giteaTestOk ? 'ok' : 'error']">{{ giteaTestMessage }}</span>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab 2: LLM 配置 -->
        <el-tab-pane label="LLM 配置" name="llm">
          <el-alert type="info" :closable="false" style="margin-bottom: 16px">
            <template #title>
              LLM Provider 仅用于 <b>builtin</b> 后端
            </template>
            此处配置的 Provider 仅对 backend 为 <b>builtin</b> 的 Agent 生效：配置后，builtin Agent 创建/编辑时可从下拉中选择 Provider 与模型。
            <b>hub-* 后端</b>（如 hub-opencode、Phase 2 的 hub-hermes）由 Hub 自身管理 LLM，<b>不读取此处配置</b>；其连接参数（URL/鉴权/工作区模式）在服务器端 agents.backends.&lt;后端名&gt; 中按命名后端统一设置，Agent 编辑页仅可覆盖提交到 Hub 的模型/Provider。
          </el-alert>
          <el-form label-width="140px" class="config-form">
            <el-form-item label="Provider 列表">
              <div class="provider-toolbar">
                <el-button type="primary" size="small" @click="openProviderDialog()">
                  <el-icon><Plus /></el-icon> 新增 Provider
                </el-button>
                <el-button size="small" @click="providerEditMode = providerEditMode === 'json' ? 'form' : 'json'">
                  <el-icon v-if="providerEditMode === 'json'"><Document /></el-icon>
                  <el-icon v-else><Edit /></el-icon>
                  {{ providerEditMode === 'json' ? '表单编辑' : 'JSON 编辑' }}
                </el-button>
                <el-tag v-if="sourceTag('llm.providers')" size="small" :type="sourceTag('llm.providers') === '数据库' ? 'success' : 'info'">
                  {{ sourceTag('llm.providers') }}
                </el-tag>
              </div>

              <!-- 表单模式：Provider 表格 -->
              <div v-if="providerEditMode === 'form'" class="provider-table-wrap">
                <el-table :data="providerList" border style="width: 100%" empty-text="暂无 Provider，点击上方按钮添加">
                  <el-table-column prop="name" label="名称" width="140" />
                  <el-table-column label="类型" width="120">
                    <template #default="{ row }">
                      <el-tag size="small" :type="row.type === 'anthropic' ? 'warning' : 'primary'">
                        {{ row.type === 'anthropic' ? 'Anthropic' : 'OpenAI 兼容' }}
                      </el-tag>
                    </template>
                  </el-table-column>
                  <el-table-column prop="base_url" label="Base URL">
                    <template #default="{ row }">
                      <span class="text-muted" v-if="!row.base_url">-</span>
                      <span v-else>{{ row.base_url }}</span>
                    </template>
                  </el-table-column>
                  <el-table-column label="API Key" width="120">
                    <template #default="{ row }">
                      <span v-if="row.api_key" class="api-key-masked">••••••••</span>
                      <span v-else class="text-muted">-</span>
                    </template>
                  </el-table-column>
                  <el-table-column label="操作" width="140" fixed="right">
                    <template #default="{ row, $index }">
                      <el-button size="small" type="primary" link @click="openProviderDialog(row, $index)">编辑</el-button>
                      <el-button size="small" type="danger" link @click="deleteProvider($index)">删除</el-button>
                    </template>
                  </el-table-column>
                </el-table>
              </div>

              <!-- JSON 模式：textarea -->
              <div v-else class="provider-json-wrap">
                <el-input
                  v-model="providersJson"
                  type="textarea"
                  :rows="10"
                  placeholder='{"deepseek":{"base_url":"https://api.deepseek.com/v1","api_key":"sk-xxx"}}'
                  @input="onProvidersJsonInput"
                />
                <div class="form-tip">
                  字段名使用 <code>base_url</code> 与 <code>api_key</code>；已保存的 api_key 以 <code>********</code> 掩码显示，保持掩码不变则沿用原 Key
                  <el-button type="primary" link size="small" class="help-link" @click="$refs.providerHelp.show()">查看配置示例</el-button>
                  <span v-if="providerNames.length" class="provider-tags">
                    已识别：{{ providerNames.join('、') }}
                  </span>
                </div>
              </div>
            </el-form-item>
            <el-form-item label="默认 Provider">
              <el-select v-model="form['llm.defaults.provider']" placeholder="选择默认 Provider" style="width: 100%">
                <el-option v-for="(_, name) in providers" :key="name" :label="name" :value="name" />
              </el-select>
              <div class="form-tip">
                <el-tag v-if="sourceTag('llm.defaults.provider')" size="small" :type="sourceTag('llm.defaults.provider') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('llm.defaults.provider') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label="默认模型">
              <el-input v-model="form['llm.defaults.model']" placeholder="deepseek-v4-flash" />
              <div class="form-tip">
                <el-tag v-if="sourceTag('llm.defaults.model')" size="small" :type="sourceTag('llm.defaults.model') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('llm.defaults.model') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label=" ">
              <el-button :loading="testingLLM" @click="testLLM">测试 LLM 连接</el-button>
              <span v-if="llmTestMessage" :class="['test-result', llmTestOk ? 'ok' : 'error']">{{ llmTestMessage }}</span>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab 3: 任务调度 -->
        <el-tab-pane label="任务调度" name="dispatcher">
          <el-alert title="调整任务执行的并发和重试参数；任务超时由 Agent 配置控制" type="info" :closable="false" style="margin-bottom: 16px" />
          <el-form label-width="140px" class="config-form">
            <el-form-item label="最大并发数">
              <el-input-number v-model.number="form['dispatcher.max_concurrent']" :min="1" :max="20" />
              <div class="form-tip">同时执行的 Agent 任务数量（默认 3）</div>
            </el-form-item>
            <el-form-item label="任务重试次数">
              <el-input-number v-model.number="form['dispatcher.task_retry_count']" :min="0" :max="5" />
              <div class="form-tip">整任务失败后自动重试次数（clone/runner 整次；默认 1）</div>
            </el-form-item>
            <el-form-item label="429 退避时间">
              <el-input-number v-model.number="form['dispatcher.rate_limit_backoff']" :min="0" :max="300" :step="5" />
              <div class="form-tip">LLM 返回 429 时等待秒数后再重试；0 表示关闭（默认 0）</div>
            </el-form-item>
            <el-form-item label="429 重试次数">
              <el-input-number v-model.number="form['llm.rate_limit_retries']" :min="0" :max="10" />
              <div class="form-tip">单次 ChatCompletion 遇 429 后的重试次数（需退避 &gt; 0；默认 1）</div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab 4: Agent 默认参数 -->
        <el-tab-pane label="Agent 默认参数" name="agents">
          <el-alert title="新建 Agent 时的默认参数，可在 Agent 编辑中单独覆盖" type="info" :closable="false" style="margin-bottom: 16px" />
          <el-form label-width="160px" class="config-form">
            <el-form-item label="默认 Provider">
              <el-select v-model="form['agents.defaults.provider']" placeholder="选择默认 Provider" style="width: 100%">
                <el-option v-for="(_, name) in providers" :key="name" :label="name" :value="name" />
              </el-select>
            </el-form-item>
            <el-form-item label="默认模型">
              <el-input v-model="form['agents.defaults.model']" placeholder="deepseek-v4-flash" />
            </el-form-item>

            <el-divider content-position="left">LLM Token</el-divider>
            <el-form-item label="最大输出 Tokens">
              <el-input-number v-model.number="form['agents.defaults.max_output_tokens']" :min="256" :max="128000" :step="512" />
              <div class="form-tip">无模型元数据时的系统兜底（当前默认 8192）；Agent 设为 0 且有模型元数据时优先用模型上限</div>
            </el-form-item>
            <el-form-item label="最大输入 Tokens">
              <el-input-number v-model.number="form['agents.defaults.max_input_tokens']" :min="1024" :max="2000000" :step="1024" />
              <div class="form-tip">无模型元数据时的系统兜底（当前默认 115200 ≈ 128K×90%）；有模型元数据时 Agent=0 走模型上下文 90%</div>
            </el-form-item>
            <el-form-item label="Temperature">
              <el-slider v-model.number="form['agents.defaults.temperature']" :min="0" :max="2" :step="0.1" show-input style="width: 100%" />
            </el-form-item>
            <el-form-item label="单次任务超时">
              <el-input v-model="form['agents.defaults.timeout']" placeholder="5m" style="width: 200px" />
              <div class="form-tip">analyze / review / reply 等单次任务总超时（Go duration，如 5m）</div>
            </el-form-item>

            <el-divider content-position="left">Agent Loop 默认参数</el-divider>
            <el-form-item label="最大迭代轮数">
              <el-input-number v-model.number="form['agents.loop.max_iterations']" :min="1" :max="100" />
            </el-form-item>
            <el-form-item label="Loop 总超时">
              <el-input v-model="form['agents.loop.total_timeout']" placeholder="30m" style="width: 200px" />
              <div class="form-tip">仅 solve / fix_bug 等多轮任务使用</div>
            </el-form-item>
            <el-form-item label="轮次间隔">
              <el-input-number v-model.number="form['agents.loop.iteration_interval']" :min="0" :max="300" :step="1" />
              <div class="form-tip">每轮 Agent Loop 之间的等待秒数；0 表示不等待</div>
            </el-form-item>

            <el-divider content-position="left">Harness 验证门禁</el-divider>
            <el-form-item label="无进展退出上限">
              <el-input-number v-model.number="form['agents.loop.no_progress_limit']" :min="0" :max="100" />
              <div class="form-tip">连续 N 轮工具调用后工作区指纹（git status --porcelain）不变则退出；0 = 关闭检测（config.example.yaml 默认 3）</div>
            </el-form-item>
            <el-form-item label="校验命令">
              <el-input
                v-model="form['agents.loop.verify_commands']"
                type="textarea"
                :rows="4"
                placeholder='每行一条命令，例如：
go test ./...
npm test'
              />
              <div class="form-tip">编码完成后、commit/PR 前执行的 shell 命令；任一命令失败则任务 failed，不写回 PR；空则跳过校验</div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab 5: 工作流策略 -->
        <el-tab-pane label="工作流策略" name="workflow">
          <el-alert title="控制工作流各阶段的约束规则，如是否强制先分析再开发、开发中能否重新分析等" type="info" :closable="false" style="margin-bottom: 16px" />
          <el-form label-width="140px" class="config-form">
            <el-form-item label="策略预设">
              <el-select v-model="form['workflow.preset']" placeholder="选择预设" style="width: 100%" @change="onPresetChange">
                <el-option
                  v-for="p in workflowPresets"
                  :key="p.name"
                  :label="`${p.label} (${p.name})`"
                  :value="p.name"
                >
                  <div class="preset-option">
                    <span class="preset-option-label">{{ p.label }}</span>
                    <span class="preset-option-desc">{{ p.description }}</span>
                  </div>
                </el-option>
              </el-select>
              <div class="form-tip">
                选择预设会自动配置各 Gate 的严格程度。也可以在下方手动调整单个 Gate。
                <el-tag v-if="sourceTag('workflow.preset')" size="small" :type="sourceTag('workflow.preset') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('workflow.preset') }}
                </el-tag>
              </div>
            </el-form-item>

            <el-divider content-position="left">Gate 详细配置</el-divider>
            <el-form-item label="开发需先分析">
              <el-select v-model="gatesForm['coder_requires_analyzed']" style="width: 200px">
                <el-option label="关闭 (off)" value="off" />
                <el-option label="警告 (soft)" value="soft" />
                <el-option label="阻止 (hard)" value="hard" />
              </el-select>
              <div class="form-tip">关闭：可直接 Assign coder；警告：可继续但会提示；阻止：必须先分析</div>
            </el-form-item>
            <el-form-item label="允许跳过分析">
              <el-select v-model="gatesForm['allow_skip_analyze']" style="width: 200px">
                <el-option label="是 (true)" value="true" />
                <el-option label="否 (false)" value="false" />
              </el-select>
              <div class="form-tip">是否允许在评论中使用 /skip-analyze 跳过分析阶段</div>
            </el-form-item>
            <el-form-item label="开发中重新分析">
              <el-select v-model="gatesForm['reanalyze_while_developing']" style="width: 200px">
                <el-option label="关闭 (off)" value="off" />
                <el-option label="警告 (soft)" value="soft" />
                <el-option label="阻止 (hard)" value="hard" />
              </el-select>
              <div class="form-tip">开发阶段中 Assign analyze 的行为</div>
            </el-form-item>
            <el-form-item label="重复执行同一阶段">
              <el-select v-model="gatesForm['rerun_same_stage']" style="width: 200px">
                <el-option label="关闭 (off)" value="off" />
                <el-option label="警告 (soft)" value="soft" />
                <el-option label="阻止 (hard)" value="hard" />
              </el-select>
              <div class="form-tip">同一阶段重复执行（如再次 Assign 同一角色）</div>
            </el-form-item>
            <el-form-item label="Draft PR 审查警告">
              <el-select v-model="gatesForm['review_warn_if_draft']" style="width: 200px">
                <el-option label="关闭 (off)" value="off" />
                <el-option label="警告 (soft)" value="soft" />
                <el-option label="阻止 (hard)" value="hard" />
              </el-select>
              <div class="form-tip">对 Draft 状态的 PR 进行审查时的行为</div>
            </el-form-item>
            <el-form-item label="切换 Agent">
              <el-select v-model="gatesForm['coder_switch_agent']" style="width: 200px">
                <el-option label="关闭 (off)" value="off" />
                <el-option label="警告 (soft)" value="soft" />
                <el-option label="阻止 (hard)" value="hard" />
              </el-select>
              <div class="form-tip">开发阶段中切换不同 Agent 的行为</div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab 6: 调试 -->
        <el-tab-pane label="调试" name="debug">
          <el-alert title="调试功能默认关闭。开启后会将 Agent Loop 的 LLM 对话写入数据库，便于排查问题。" type="warning" :closable="false" style="margin-bottom: 16px" />
          <el-form label-width="180px" class="config-form">
            <el-form-item label="记录 Agent 对话">
              <el-switch v-model="form['debug.conversation_log.enabled']" />
              <div class="form-tip">
                开启后，solve / fix_bug 等多轮任务的每轮 LLM 消息与 tool call 将持久化到 <code>task_conversation_logs</code> 表；可在「任务」详情或「对话」入口查看
                <el-tag v-if="sourceTag('debug.conversation_log.enabled')" size="small" :type="sourceTag('debug.conversation_log.enabled') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('debug.conversation_log.enabled') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label="单条内容最大字符">
              <el-input-number v-model.number="form['debug.conversation_log.max_content_chars']" :min="0" :max="500000" :step="10000" />
              <div class="form-tip">写入数据库前截断 message / tool result 长度；0 表示不截断（默认 100000）</div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab: Deliver 出站通知 -->
        <el-tab-pane label="Deliver 通知" name="deliver">
          <el-alert type="info" :closable="false" style="margin-bottom: 16px">
            <template #title>出站事件通知（仅出站扇出，不自研 IM SDK）</template>
            任务完成后，Matea 可将结果以 Webhook 形式推送到你自建的 IM 机器人（企业微信 / 飞书 / 钉钉）或 Hub 接收端。
            <b>OpenCode 后端无自带 IM 渠道，必须配置</b>；Hermes 自带原生渠道，可留空。未配置时静默不通知（结果仍正常写回 Gitea）。
          </el-alert>
          <el-form label-width="160px" class="config-form">
            <el-form-item label="Webhook URL">
              <el-input
                v-model="form['deliver.webhook_url']"
                placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx"
              />
              <div class="form-tip">
                出站事件的唯一接收地址；可指向企业微信 / 飞书 / 钉钉机器人 Webhook，或自建 Hub 接收端。留空 = 关闭通知。
                <el-tag v-if="sourceTag('deliver.webhook_url')" size="small" :type="sourceTag('deliver.webhook_url') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('deliver.webhook_url') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label="单次请求超时">
              <el-input v-model="form['deliver.timeout']" placeholder="10s（Go duration，默认 10s）" style="width: 240px" />
              <div class="form-tip">
                单个 POST 的超时时间（Go duration 语法，如 5s / 2m）；为空使用默认 10s。
                <el-tag v-if="sourceTag('deliver.timeout')" size="small" :type="sourceTag('deliver.timeout') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('deliver.timeout') }}
                </el-tag>
              </div>
            </el-form-item>
            <el-form-item label="最大重试次数">
              <el-input-number v-model.number="form['deliver.max_retries']" :min="0" :max="10" />
              <div class="form-tip">
                首次失败后额外重试次数（网络错误 / 5xx）；0 = 仅尝试一次（默认 0）。4xx 不重试。
                <el-tag v-if="sourceTag('deliver.max_retries')" size="small" :type="sourceTag('deliver.max_retries') === '数据库' ? 'success' : 'info'" style="margin-left: 8px">
                  {{ sourceTag('deliver.max_retries') }}
                </el-tag>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab 6: Prompt 模板 -->
        <el-tab-pane label="Prompt 模板" name="prompts">
          <el-alert title="管理内置 Prompt 模板。自定义模板优先级高于内置模板（DB > 内置）。" type="info" :closable="false" style="margin-bottom: 16px" />
          <div style="margin-bottom: 12px">
            <el-button type="primary" size="small" @click="showAddTemplate = true">
              <el-icon><Plus /></el-icon> 新增模板
            </el-button>
          </div>
          <el-table :data="templateList" style="width: 100%">
            <el-table-column prop="name" label="名称" width="160" />
            <el-table-column prop="source" label="来源" width="100">
              <template #default="{ row }">
                <el-tag :type="row.source === 'custom' ? 'success' : 'info'" size="small">
                  {{ row.source === 'custom' ? '自定义' : row.source === 'config' ? '配置文件' : '内置' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="system_prompt" label="System Prompt">
              <template #default="{ row }">
                <span class="prompt-preview">{{ row.system_prompt?.substring(0, 80) }}...</span>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="150">
              <template #default="{ row }">
                <el-button size="small" type="primary" link @click="viewTemplate(row)">查看</el-button>
                <el-button v-if="row.source === 'custom'" size="small" type="danger" link @click="deleteTemplate(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- 查看模板对话框 -->
    <el-dialog v-model="showViewTemplate" :title="'模板详情：' + (viewingTemplate?.name || '')" width="700px" :close-on-click-modal="false">
      <h4>System Prompt</h4>
      <el-input :model-value="viewingTemplate?.system_prompt" type="textarea" :rows="8" readonly />
      <h4 style="margin-top: 16px">User Template</h4>
      <el-input :model-value="viewingTemplate?.user_template" type="textarea" :rows="4" readonly />
    </el-dialog>

    <!-- 新增模板对话框 -->
    <el-dialog v-model="showAddTemplate" title="新增 Prompt 模板" width="700px" :close-on-click-modal="false">
      <el-form :model="newTemplate" label-width="120px">
        <el-form-item label="模板名称">
          <el-input v-model="newTemplate.name" placeholder="如 my_review" />
          <div class="form-tip">唯一标识，创建后不可修改</div>
        </el-form-item>
        <el-form-item label="System Prompt">
          <el-input v-model="newTemplate.system_prompt" type="textarea" :rows="8" placeholder="Agent 的系统提示词" />
        </el-form-item>
        <el-form-item label="User Template">
          <el-input v-model="newTemplate.user_template" type="textarea" :rows="4" placeholder="用户消息模板（支持 Go template 语法）" />
          <el-button type="primary" link size="small" style="margin-top: 4px" @click="$refs.templateHelp.show()">查看模板变量说明</el-button>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showAddTemplate = false">取消</el-button>
        <el-button type="primary" @click="addTemplate">创建</el-button>
      </template>
    </el-dialog>

    <!-- Provider 编辑对话框 -->
    <el-dialog
      v-model="providerDialogVisible"
      :title="editingProviderIndex >= 0 ? '编辑 Provider' : '新增 Provider'"
      width="560px"
      :close-on-click-modal="false"
    >
      <el-form :model="providerForm" label-width="120px">
        <el-form-item label="Provider 名称" required>
          <el-input
            v-model="providerForm.name"
            placeholder="如 deepseek、openai、ollama"
            :disabled="editingProviderIndex >= 0"
          />
          <div class="form-tip">唯一标识，创建后不可修改</div>
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="providerForm.type" style="width: 100%">
            <el-option label="OpenAI 兼容（DeepSeek、Qwen、Ollama 等）" value="openai_compatible" />
            <el-option label="Anthropic (Claude)" value="anthropic" />
          </el-select>
        </el-form-item>
        <el-form-item label="Base URL">
          <el-input
            v-model="providerForm.base_url"
            placeholder="https://api.deepseek.com/v1"
          />
          <div class="form-tip">Anthropic 可留空</div>
        </el-form-item>
        <el-form-item label="API Key" required>
          <el-input
            v-model="providerForm.api_key"
            type="password"
            show-password
            placeholder="sk-xxx"
          />
          <div class="form-tip">已保存的 Key 以 <code>********</code> 掩码显示——保持掩码不变则沿用原 Key，输入新值即替换。</div>
        </el-form-item>

        <el-collapse v-model="providerAdvancedOpen" class="provider-advanced">
          <el-collapse-item title="高级配置" name="advanced">
            <el-form-item label="模型发现模式">
              <el-radio-group v-model="providerForm.model_discovery">
                <el-radio value="auto">自动发现（调用 /models API）</el-radio>
                <el-radio value="builtin">使用内置目录</el-radio>
                <el-radio value="custom">自定义列表</el-radio>
              </el-radio-group>
              <div class="form-tip">
                自动发现：尝试调用 Provider 的 /models 接口获取模型列表；失败时回退到内置目录
              </div>
            </el-form-item>
            <el-form-item v-if="providerForm.model_discovery === 'custom'" label="自定义模型">
              <div class="model-list-toolbar">
                <el-button type="primary" size="small" @click="openModelDialog()">
                  <el-icon><Plus /></el-icon> 新增模型
                </el-button>
                <span class="form-tip" style="margin-left: 8px">共 {{ providerForm.models.length }} 个模型</span>
              </div>
              <el-table :data="providerForm.models" border size="small" style="width: 100%; margin-top: 8px" empty-text="暂无模型">
                <el-table-column prop="id" label="模型 ID" width="180" />
                <el-table-column prop="name" label="显示名" width="160" />
                <el-table-column label="上下文窗口" width="120">
                  <template #default="{ row }">
                    <span v-if="row.context_window">{{ row.context_window.toLocaleString() }}</span>
                    <span v-else class="text-muted">-</span>
                  </template>
                </el-table-column>
                <el-table-column label="最大输出" width="100">
                  <template #default="{ row }">
                    <span v-if="row.max_output">{{ row.max_output.toLocaleString() }}</span>
                    <span v-else class="text-muted">-</span>
                  </template>
                </el-table-column>
                <el-table-column label="工具调用" width="80" align="center">
                  <template #default="{ row }">
                    <el-tag v-if="row.supports_tools" size="small" type="success">支持</el-tag>
                    <span v-else class="text-muted">-</span>
                  </template>
                </el-table-column>
                <el-table-column label="推理模型" width="80" align="center">
                  <template #default="{ row }">
                    <el-tag v-if="row.is_reasoning" size="small" type="warning">是</el-tag>
                    <span v-else class="text-muted">-</span>
                  </template>
                </el-table-column>
                <el-table-column label="操作" width="140" fixed="right">
                  <template #default="{ row, $index }">
                    <el-button size="small" type="primary" link @click="openModelDialog(row, $index)">编辑</el-button>
                    <el-button size="small" type="danger" link @click="deleteModel($index)">删除</el-button>
                  </template>
                </el-table-column>
              </el-table>
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button @click="providerDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="saveProvider">保存</el-button>
      </template>
    </el-dialog>

    <!-- 模型编辑弹窗 -->
    <el-dialog
      v-model="modelDialogVisible"
      :title="editingModelIndex >= 0 ? '编辑模型' : '新增模型'"
      width="560px"
      :close-on-click-modal="false"
    >
      <el-form :model="modelForm" label-width="120px">
        <el-form-item label="模型 ID" required>
          <el-input v-model="modelForm.id" placeholder="如 deepseek-v4-flash" />
        </el-form-item>
        <el-form-item label="显示名称">
          <el-input v-model="modelForm.name" placeholder="如 DeepSeek V4" />
        </el-form-item>
        <el-form-item label="上下文窗口">
          <el-input-number v-model.number="modelForm.context_window" :min="0" :max="2000000" :step="1024" style="width: 100%" />
          <div class="form-tip">单位：tokens，0 表示未设置</div>
        </el-form-item>
        <el-form-item label="最大输出">
          <el-input-number v-model.number="modelForm.max_output" :min="0" :max="200000" :step="256" style="width: 100%" />
          <div class="form-tip">单位：tokens，0 表示未设置</div>
        </el-form-item>
        <el-form-item label="支持工具调用">
          <el-switch v-model="modelForm.supports_tools" />
        </el-form-item>
        <el-form-item label="推理模型">
          <el-switch v-model="modelForm.is_reasoning" />
        </el-form-item>
        <el-form-item label="模型描述">
          <el-input v-model="modelForm.description" type="textarea" :rows="2" placeholder="可选" />
        </el-form-item>

        <el-divider content-position="left">价格配置（$/1K tokens）</el-divider>
        <el-form-item label="输入价格">
          <el-input-number v-model.number="modelForm.input_price" :min="0" :max="1" :step="0.0001" :precision="4" style="width: 100%" />
          <div class="form-tip">输入 tokens 的价格（$/1K），0 表示未设置</div>
        </el-form-item>
        <el-form-item label="输出价格">
          <el-input-number v-model.number="modelForm.output_price" :min="0" :max="1" :step="0.0001" :precision="4" style="width: 100%" />
          <div class="form-tip">输出 tokens 的价格（$/1K），0 表示未设置</div>
        </el-form-item>

        <el-collapse v-model="modelAdvancedOpen" class="model-advanced">
          <el-collapse-item title="模型级默认参数" name="default_params">
            <el-form-item label="Temperature">
              <el-slider v-model.number="modelForm.default_params.temperature" :min="0" :max="2" :step="0.1" show-input style="width: 100%" />
              <div class="form-tip">控制随机性，0=确定性，2=最大随机性</div>
            </el-form-item>
            <el-form-item label="Top P">
              <el-slider v-model.number="modelForm.default_params.top_p" :min="0" :max="1" :step="0.05" show-input style="width: 100%" />
              <div class="form-tip">核采样，与 temperature 二选一，建议不要同时调整</div>
            </el-form-item>
            <el-form-item label="最大输出 Tokens">
              <el-input-number v-model.number="modelForm.default_params.max_output_tokens" :min="0" :max="128000" :step="256" style="width: 100%" />
              <div class="form-tip">覆盖 Provider 级默认值，0 表示不覆盖</div>
            </el-form-item>
            <el-form-item label="频率惩罚">
              <el-slider v-model.number="modelForm.default_params.frequency_penalty" :min="-2" :max="2" :step="0.1" show-input style="width: 100%" />
              <div class="form-tip">降低重复词汇的概率</div>
            </el-form-item>
            <el-form-item label="存在惩罚">
              <el-slider v-model.number="modelForm.default_params.presence_penalty" :min="-2" :max="2" :step="0.1" show-input style="width: 100%" />
              <div class="form-tip">增加未出现词汇的概率</div>
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button @click="modelDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="saveModel">保存</el-button>
      </template>
    </el-dialog>

    <TemplateHelp ref="templateHelp" />
    <ProviderConfigHelp ref="providerHelp" />
  </div>
</template>

<script setup>
import { ref, onMounted, computed, watch } from 'vue'
import api from '../api'
import { Check, Plus, Document, Edit } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import TemplateHelp from '../components/TemplateHelp.vue'
import ProviderConfigHelp from '../components/ProviderConfigHelp.vue'

const activeTab = ref('gitea')
const form = ref({})
const sources = ref({})
const saving = ref(false)
const testingGitea = ref(false)
const testingLLM = ref(false)
const giteaTestMessage = ref('')
const giteaTestOk = ref(false)
const llmTestMessage = ref('')
const llmTestOk = ref(false)
const providersJson = ref('')
const templateList = ref([])
const showViewTemplate = ref(false)
const viewingTemplate = ref(null)
const showAddTemplate = ref(false)
const newTemplate = ref({ name: '', system_prompt: '', user_template: '' })

// Workflow presets
const workflowPresets = ref([
  { name: 'free', label: '自由模式', description: '最小限制，允许跳过分析直接开发' },
  { name: 'standard', label: '标准模式', description: '平衡配置，开发中重新分析会警告' },
  { name: 'strict', label: '严格模式', description: '最大限制，强制分析后才能开发' },
])

// Gates form
const gatesForm = ref({
  coder_requires_analyzed: 'off',
  allow_skip_analyze: 'true',
  reanalyze_while_developing: 'off',
  rerun_same_stage: 'off',
  review_warn_if_draft: 'off',
  coder_switch_agent: 'off',
})

// Provider 可视化编辑状态
const providerEditMode = ref('form') // form | json
const providerDialogVisible = ref(false)
const editingProviderIndex = ref(-1)
const providerAdvancedOpen = ref([])
const providerForm = ref({
  name: '',
  type: 'openai_compatible',
  base_url: '',
  api_key: '',
  model_discovery: 'builtin', // auto | builtin | custom
  models: []
})

// 模型编辑弹窗状态
const modelDialogVisible = ref(false)
const editingModelIndex = ref(-1)
const modelAdvancedOpen = ref([])
const modelForm = ref({
  id: '',
  name: '',
  context_window: 0,
  max_output: 0,
  supports_tools: false,
  is_reasoning: false,
  description: '',
  input_price: 0,
  output_price: 0,
  default_params: {
    temperature: null,
    top_p: null,
    max_output_tokens: null,
    frequency_penalty: null,
    presence_penalty: null
  }
})

const providerList = computed(() => {
  const map = providers.value
  return Object.entries(map).map(([name, cfg]) => ({
    name,
    type: cfg.type || 'openai_compatible',
    base_url: cfg.base_url || '',
    api_key: cfg.api_key || ''
  }))
})

const providers = computed(() => {
  try {
    return normalizeProviders(JSON.parse(providersJson.value))
  } catch {
    return {}
  }
})

const providerNames = computed(() => Object.keys(providers.value))

const normalizeProviders = (raw) => {
  const out = {}
  for (const [name, cfg] of Object.entries(raw || {})) {
    if (!cfg || typeof cfg !== 'object') continue
    out[name] = {
      base_url: cfg.base_url || cfg.BaseURL || '',
      api_key: cfg.api_key || cfg.APIKey || '',
      type: cfg.type || 'openai_compatible',
      models: cfg.models || undefined,
      default_params: cfg.default_params || undefined
    }
  }
  return out
}

const formatProvidersJson = (raw) => JSON.stringify(normalizeProviders(raw), null, 2)

// 打开 Provider 编辑对话框
const openProviderDialog = (row = null, index = -1) => {
  editingProviderIndex.value = index
  if (row) {
    providerForm.value = {
      name: row.name,
      type: row.type || 'openai_compatible',
      base_url: row.base_url || '',
      api_key: row.api_key || '',
      model_discovery: 'builtin',
      models: []
    }
    // 检测模型发现模式
    const cfg = providers.value[row.name]
    if (cfg) {
      if (cfg.models && Array.isArray(cfg.models) && cfg.models.length > 0) {
        providerForm.value.model_discovery = 'custom'
        providerForm.value.models = cfg.models.map(m => ({
          id: m.id || m,
          name: m.name || m.id || m,
          context_window: m.context_window || 0,
          max_output: m.max_output || 0,
          supports_tools: !!m.supports_tools,
          is_reasoning: !!m.is_reasoning,
          description: m.description || '',
          input_price: m.input_price || 0,
          output_price: m.output_price || 0,
          default_params: m.default_params || {}
        }))
      } else if (cfg.models && Array.isArray(cfg.models) && cfg.models.length === 0) {
        providerForm.value.model_discovery = 'auto'
      } else {
        providerForm.value.model_discovery = 'builtin'
      }
    }
  } else {
    providerForm.value = {
      name: '',
      type: 'openai_compatible',
      base_url: '',
      api_key: '',
      model_discovery: 'builtin',
      models: []
    }
  }
  providerAdvancedOpen.value = []
  providerDialogVisible.value = true
}

// 保存 Provider
const saveProvider = () => {
  const name = providerForm.value.name.trim()
  if (!name) {
    ElMessage.warning('请填写 Provider 名称')
    return
  }
  if (!providerForm.value.api_key.trim()) {
    ElMessage.warning('请填写 API Key')
    return
  }

  // 检查名称重复（新增时）
  if (editingProviderIndex.value < 0 && providers.value[name]) {
    ElMessage.warning('Provider 名称已存在')
    return
  }

  const current = JSON.parse(providersJson.value || '{}')
  const entry = {
    base_url: providerForm.value.base_url.trim(),
    api_key: providerForm.value.api_key.trim(),
    type: providerForm.value.type
  }

  // 根据模型发现模式设置 models 字段
  switch (providerForm.value.model_discovery) {
    case 'auto':
      entry.models = []
      break
    case 'custom':
      entry.models = providerForm.value.models.map(m => {
        const out = { id: m.id }
        if (m.name) out.name = m.name
        if (m.context_window > 0) out.context_window = m.context_window
        if (m.max_output > 0) out.max_output = m.max_output
        if (m.supports_tools) out.supports_tools = true
        if (m.is_reasoning) out.is_reasoning = true
        if (m.description) out.description = m.description
        return out
      })
      break
    // builtin: 不设置 models 字段
  }

  current[name] = entry
  providersJson.value = formatProvidersJson(current)
  providerDialogVisible.value = false
  ElMessage.success(editingProviderIndex.value >= 0 ? '已更新 Provider' : '已添加 Provider')
}

// 打开模型编辑弹窗
const openModelDialog = (row = null, index = -1) => {
  editingModelIndex.value = index
  if (row) {
    modelForm.value = {
      id: row.id || '',
      name: row.name || '',
      context_window: row.context_window || 0,
      max_output: row.max_output || 0,
      supports_tools: !!row.supports_tools,
      is_reasoning: !!row.is_reasoning,
      description: row.description || '',
      input_price: row.input_price || 0,
      output_price: row.output_price || 0,
      default_params: {
        temperature: row.default_params?.temperature ?? null,
        top_p: row.default_params?.top_p ?? null,
        max_output_tokens: row.default_params?.max_output_tokens ?? null,
        frequency_penalty: row.default_params?.frequency_penalty ?? null,
        presence_penalty: row.default_params?.presence_penalty ?? null
      }
    }
  } else {
    modelForm.value = {
      id: '',
      name: '',
      context_window: 0,
      max_output: 0,
      supports_tools: false,
      is_reasoning: false,
      description: '',
      input_price: 0,
      output_price: 0,
      default_params: {
        temperature: null,
        top_p: null,
        max_output_tokens: null,
        frequency_penalty: null,
        presence_penalty: null
      }
    }
  }
  modelAdvancedOpen.value = []
  modelDialogVisible.value = true
}

// 保存模型
const saveModel = () => {
  const id = modelForm.value.id.trim()
  if (!id) {
    ElMessage.warning('请填写模型 ID')
    return
  }

  // 检查 ID 重复（新增时）
  if (editingModelIndex.value < 0 && providerForm.value.models.some(m => m.id === id)) {
    ElMessage.warning('模型 ID 已存在')
    return
  }

  const modelData = {
    id,
    name: modelForm.value.name.trim(),
    context_window: modelForm.value.context_window || 0,
    max_output: modelForm.value.max_output || 0,
    supports_tools: !!modelForm.value.supports_tools,
    is_reasoning: !!modelForm.value.is_reasoning,
    description: modelForm.value.description.trim(),
    input_price: modelForm.value.input_price || 0,
    output_price: modelForm.value.output_price || 0
  }

  // 仅当有非空默认参数时才添加
  const dp = modelForm.value.default_params
  const hasDefaultParams = dp.temperature !== null || dp.top_p !== null || 
    dp.max_output_tokens !== null || dp.frequency_penalty !== null || dp.presence_penalty !== null
  if (hasDefaultParams) {
    modelData.default_params = {}
    if (dp.temperature !== null) modelData.default_params.temperature = dp.temperature
    if (dp.top_p !== null) modelData.default_params.top_p = dp.top_p
    if (dp.max_output_tokens !== null) modelData.default_params.max_output_tokens = dp.max_output_tokens
    if (dp.frequency_penalty !== null) modelData.default_params.frequency_penalty = dp.frequency_penalty
    if (dp.presence_penalty !== null) modelData.default_params.presence_penalty = dp.presence_penalty
  }

  if (editingModelIndex.value >= 0) {
    providerForm.value.models.splice(editingModelIndex.value, 1, modelData)
  } else {
    providerForm.value.models.push(modelData)
  }

  modelDialogVisible.value = false
  ElMessage.success(editingModelIndex.value >= 0 ? '已更新模型' : '已添加模型')
}

// 删除模型
const deleteModel = (index) => {
  providerForm.value.models.splice(index, 1)
  ElMessage.success('已删除模型')
}

// 删除 Provider
const deleteProvider = async (index) => {
  const row = providerList.value[index]
  try {
    await ElMessageBox.confirm(`确定删除 Provider "${row.name}"？`, '确认', { type: 'warning' })
    const current = JSON.parse(providersJson.value || '{}')
    delete current[row.name]
    providersJson.value = formatProvidersJson(current)
    ElMessage.success('已删除')
  } catch {
    // cancel
  }
}

// JSON 输入时同步（防止格式错误时丢失数据）
const onProvidersJsonInput = () => {
  // 无需额外处理，providers computed 会自动解析
}

const setupHint = computed(() => {
  const fileCount = Object.values(sources.value).filter(v => v === 'file').length
  if (fileCount === 0) return ''
  return `有 ${fileCount} 项配置来自 config.yaml，保存后将写入数据库。建议先完成 Gitea 连接测试，再配置 LLM，最后到 Agent 管理创建 Agent。`
})

const sourceTag = (key) => {
  const src = sources.value[key]
  if (!src) return ''
  return src === 'db' ? '数据库' : 'config.yaml'
}

const applyConfigData = (data) => {
  const next = { ...data }
  if (next._meta?.sources) {
    sources.value = next._meta.sources
  }
  if (next._meta?.workflow_presets) {
    workflowPresets.value = next._meta.workflow_presets
  }
  if (next._meta) {
    delete next._meta
  }
  if (next['debug.conversation_log.enabled'] === undefined) {
    next['debug.conversation_log.enabled'] = false
  }
  if (next['debug.conversation_log.max_content_chars'] === undefined) {
    next['debug.conversation_log.max_content_chars'] = 100000
  }
  if (next['agents.loop.verify_commands'] !== undefined) {
    const cmds = next['agents.loop.verify_commands']
    if (Array.isArray(cmds)) {
      next['agents.loop.verify_commands'] = cmds.join('\n')
    } else {
      try {
        const parsed = JSON.parse(cmds)
        if (Array.isArray(parsed)) {
          next['agents.loop.verify_commands'] = parsed.join('\n')
        }
      } catch {
        // keep as-is if not valid JSON
      }
    }
  }
  form.value = next
  if (data['llm.providers']) {
    providersJson.value = formatProvidersJson(data['llm.providers'])
  }
  if (data['workflow.gates'] && typeof data['workflow.gates'] === 'object') {
    gatesForm.value = { ...gatesForm.value, ...data['workflow.gates'] }
  }
}

const onPresetChange = (preset) => {
  const presetGates = {
    free: {
      coder_requires_analyzed: 'off',
      allow_skip_analyze: 'true',
      reanalyze_while_developing: 'off',
      rerun_same_stage: 'off',
      review_warn_if_draft: 'off',
      coder_switch_agent: 'off',
    },
    standard: {
      coder_requires_analyzed: 'off',
      allow_skip_analyze: 'true',
      reanalyze_while_developing: 'soft',
      rerun_same_stage: 'soft',
      review_warn_if_draft: 'off',
      coder_switch_agent: 'soft',
    },
    strict: {
      coder_requires_analyzed: 'hard',
      allow_skip_analyze: 'false',
      reanalyze_while_developing: 'hard',
      rerun_same_stage: 'hard',
      review_warn_if_draft: 'soft',
      coder_switch_agent: 'hard',
    },
  }
  const gates = presetGates[preset] || presetGates.standard
  gatesForm.value = { ...gates }
}

const loadConfig = async () => {
  const data = await api.get('/config')
  applyConfigData(data)
}

const loadTemplates = async () => {
  try {
    const data = await api.get('/prompt-templates')
    templateList.value = Object.entries(data).map(([key, val]) => ({
      name: key,
      ...val
    }))
  } catch {
    templateList.value = []
  }
}

const viewTemplate = (row) => {
  viewingTemplate.value = row
  showViewTemplate.value = true
}

const addTemplate = async () => {
  if (!newTemplate.value.name || !newTemplate.value.system_prompt) {
    ElMessage.warning('请填写模板名称和 System Prompt')
    return
  }
  try {
    const payload = {}
    payload[newTemplate.value.name] = {
      name: newTemplate.value.name,
      system_prompt: newTemplate.value.system_prompt,
      user_template: newTemplate.value.user_template || ''
    }
    await api.put('/prompt-templates', payload)
    ElMessage.success('模板创建成功')
    showAddTemplate.value = false
    newTemplate.value = { name: '', system_prompt: '', user_template: '' }
    await loadTemplates()
  } catch (error) {
    ElMessage.error(error.response?.data?.error || '创建失败')
  }
}

const deleteTemplate = async (row) => {
  try {
    await ElMessageBox.confirm(`确定删除模板"${row.name}"？`, '确认')
    await api.delete(`/prompt-templates/${row.name}`)
    ElMessage.success('删除成功')
    await loadTemplates()
  } catch (error) {
    if (error !== 'cancel') ElMessage.error('删除失败')
  }
}

const saveAll = async () => {
  saving.value = true
  try {
    // Parse providers JSON
    let providersData
    try {
      providersData = normalizeProviders(JSON.parse(providersJson.value))
    } catch {
      ElMessage.error('Provider 配置 JSON 格式错误')
      saving.value = false
      return
    }
    if (Object.keys(providersData).length === 0) {
      ElMessage.error('请至少配置一个 Provider')
      saving.value = false
      return
    }

    // Build entries to save
    const entries = {}
    for (const [key, value] of Object.entries(form.value)) {
      if (key === 'llm.providers') continue // handle separately
      if (value === null || value === undefined) continue
      if (value === '' && typeof value !== 'boolean') continue
      if (key === 'agents.loop.verify_commands') {
        const cmds = value.split('\n').map(s => s.trim()).filter(Boolean)
        entries[key] = JSON.stringify(cmds)
      } else {
        entries[key] = String(value)
      }
    }
    entries['llm.providers'] = JSON.stringify(providersData)
    entries['workflow.gates'] = JSON.stringify(gatesForm.value)

    const data = await api.put('/config', entries)
    applyConfigData(data)
    ElMessage.success('配置已保存')
  } catch (error) {
    ElMessage.error(error.response?.data?.error || '保存失败')
  } finally {
    saving.value = false
  }
}

const testGitea = async () => {
  testingGitea.value = true
  giteaTestMessage.value = ''
  try {
    const result = await api.post('/config/test/gitea', {
      'gitea.url': form.value['gitea.url'] || '',
      'gitea.admin_token': form.value['gitea.admin_token'] || ''
    })
    giteaTestOk.value = !!result.ok
    giteaTestMessage.value = result.message
  } catch (error) {
    giteaTestOk.value = false
    giteaTestMessage.value = error.response?.data?.message || error.response?.data?.error || '测试失败'
  } finally {
    testingGitea.value = false
  }
}

const testLLM = async () => {
  testingLLM.value = true
  llmTestMessage.value = ''
  try {
    let providersData
    try {
      providersData = normalizeProviders(JSON.parse(providersJson.value))
    } catch {
      ElMessage.error('Provider 配置 JSON 格式错误')
      testingLLM.value = false
      return
    }
    const result = await api.post('/config/test/llm', {
      'llm.defaults.provider': form.value['llm.defaults.provider'] || '',
      'llm.defaults.model': form.value['llm.defaults.model'] || '',
      'agents.defaults.max_output_tokens': form.value['agents.defaults.max_output_tokens'],
      'llm.providers': providersData
    })
    llmTestOk.value = !!result.ok
    llmTestMessage.value = result.message
  } catch (error) {
    llmTestOk.value = false
    llmTestMessage.value = error.response?.data?.message || error.response?.data?.error || '测试失败'
  } finally {
    testingLLM.value = false
  }
}

onMounted(() => {
  loadConfig()
  loadTemplates()
})
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.config-form {
  max-width: 700px;
}

.form-tip {
  font-size: 12px;
  color: #909399;
  margin-top: 6px;
  line-height: 1.5;
}

/* el-form-item__content 为 flex；提示单独占一行，避免贴在输入控件右侧 */
.el-form-item__content > .form-tip {
  flex-basis: 100%;
  width: 100%;
}

.prompt-preview {
  max-width: 400px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 13px;
  color: #606266;
}

.test-result {
  margin-left: 12px;
  font-size: 13px;
}

.test-result.ok {
  color: #67c23a;
}

.test-result.error {
  color: #f56c6c;
}

.form-tip code {
  font-size: 12px;
}

.help-link {
  margin-left: 8px;
  padding: 0;
  vertical-align: baseline;
}

.provider-tags {
  display: block;
  margin-top: 4px;
  color: #67c23a;
}

.provider-toolbar {
  margin-bottom: 12px;
  display: flex;
  gap: 8px;
  align-items: center;
}

.provider-table-wrap {
  margin-top: 8px;
}

.provider-json-wrap {
  margin-top: 8px;
}

.api-key-masked {
  font-family: monospace;
  letter-spacing: 2px;
}

.provider-advanced {
  margin-top: 8px;
}

.model-list-toolbar {
  display: flex;
  align-items: center;
  margin-bottom: 4px;
}

.text-muted {
  color: #c0c4cc;
}

.preset-option {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.preset-option-label {
  font-weight: 500;
}

.preset-option-desc {
  font-size: 12px;
  color: #909399;
}
</style>
