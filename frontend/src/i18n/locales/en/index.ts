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
    name: 'Alibaba Cloud Bailian',
    baseUrlRequired: 'Enter the Bailian origin containing your workspace ID, in the same region as the API key',
    baseUrlHint: 'Use https://WORKSPACE_ID.cn-beijing.maas.aliyuncs.com or the documented origin for your region. Connection tests only query models and do not generate paid videos.',
    billingHint: 'Wan 3 bills actual input-reference plus output video seconds, preserving fractions, up to 30 seconds total. Set an explicit USD/second price; CNY supplier prices are not automatically converted.',
    modeHint: 'Successful tasks settle once from reported usage; failed tasks do not bill. No separate surcharge is added for images, reference audio, or the output-audio toggle.',
    modelPricesHint: 'Price standard, prime, and custom aliases separately. Model rates override common resolution rates; input and output video share the same per-second rate.',
    inputSeconds: 'Input video',
    outputSeconds: 'Output video',
    billableSeconds: 'Billable total',
    billingMode: 'Video per second',
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
