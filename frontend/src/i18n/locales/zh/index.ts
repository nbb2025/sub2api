import adminAccessKeys from './adminAccessKeys'
import mcp from './mcp'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import misc from './misc'

export default {
  bailian: {
    name: '阿里云百炼',
    baseUrlRequired: '请填写与 API Key 同地域、包含业务空间 ID 的百炼根地址',
    baseUrlHint: '填写 https://业务空间ID.cn-beijing.maas.aliyuncs.com；其他地域按百炼文档选择。连接测试只查询模型，不生成收费视频。',
    billingHint: 'Wan 3 按输入参考视频＋输出视频的实际总秒数计费，保留小数，最多合计 30 秒。请明确配置 USD/秒单价；不会自动将阿里云人民币价格换算成美元。',
    modeHint: '仅成功任务按上游实际用量结算一次；失败任务不计费。图片、参考音频及输出声音开关不另外加价。',
    modelPricesHint: '标准版、高速版及自定义别名可分别定价。模型单价优先于通用分辨率价格；输入、输出视频使用同一每秒单价。',
    inputSeconds: '输入视频',
    outputSeconds: '输出视频',
    billableSeconds: '计费总时长',
    billingMode: '按秒（视频）',
  },
  adminAccessKeys,
  mcp,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  admin,
  ...misc,
}
