import { hfsyVideoModels } from '@/constants/hfsyVideoModels'

export const grokVideoPriceResolutions = [
  { key: '480p', label: '480p' },
  { key: '720p', label: '720p' },
  { key: '1080p', label: '1080p' }
] as const

export const grokVideoPriceFamilies = [
  { key: 'grok-imagine-video', label: 'grok-imagine-video' },
  { key: 'grok-imagine-video-1.5', label: 'grok-imagine-video-1.5' }
] as const

export const bailianVideoPriceFamilies = [
  { key: 'wan3.0-video', label: 'wan3.0-video' },
  { key: 'wan3.0-video-prime', label: 'wan3.0-video-prime' }
] as const

export type VideoModelPrices = Record<string, Record<string, number>>
export type VideoModelPricesForm = Record<string, Record<string, number | string | null>>

function normalizeFamily(value: string): string {
  return value.trim().toLowerCase()
}

function normalizePrice(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const price = Number(value)
  return Number.isFinite(price) && price >= 0 ? price : null
}

function emptyTiers(): Record<string, number | string | null> {
  return Object.fromEntries(grokVideoPriceResolutions.map(({ key }) => [key, null]))
}

// Keep unknown families from an existing group so a future backend catalog is
// not silently discarded when an operator edits another group setting.
export function createVideoModelPricesForm(
  prices?: VideoModelPrices | VideoModelPricesForm | null,
  platform = 'grok'
): VideoModelPricesForm {
  const form: VideoModelPricesForm = {}

  for (const [rawFamily, rawTiers] of Object.entries(prices ?? {})) {
    const family = normalizeFamily(rawFamily)
    if (!family || !rawTiers || typeof rawTiers !== 'object') continue
    form[family] = emptyTiers()
    for (const [rawResolution, rawPrice] of Object.entries(rawTiers)) {
      const price = normalizePrice(rawPrice)
      if (price !== null) form[family][rawResolution.trim().toLowerCase()] = price
    }
  }

  for (const { key } of videoPriceFamilies(platform)) {
    form[key] ??= emptyTiers()
  }
  return form
}

export function serializeVideoModelPrices(form: VideoModelPricesForm): VideoModelPrices {
  const result: VideoModelPrices = {}
  for (const [rawFamily, tiers] of Object.entries(form)) {
    const family = normalizeFamily(rawFamily)
    if (!family || !tiers || typeof tiers !== 'object') continue

    const normalizedTiers: Record<string, number> = {}
    for (const [rawResolution, rawPrice] of Object.entries(tiers)) {
      const resolution = rawResolution.trim().toLowerCase()
      const price = normalizePrice(rawPrice)
      if (resolution && price !== null) normalizedTiers[resolution] = price
    }
    if (Object.keys(normalizedTiers).length > 0) result[family] = normalizedTiers
  }
  return result
}

function videoPriceFamilies(platform: string) {
  if (platform === 'video-lt') return hfsyVideoModels.map(({ id }) => ({ key: id, label: id }))
  return platform === 'bailian' ? bailianVideoPriceFamilies : grokVideoPriceFamilies
}

export function videoModelPriceResolutions(platform: string, model: string) {
  if (platform !== 'video-lt') return grokVideoPriceResolutions
  const spec = hfsyVideoModels.find(({ id }) => id === model)
  return spec ? grokVideoPriceResolutions.filter(({ key }) => key === spec.resolution) : grokVideoPriceResolutions
}

export function videoModelPriceFamilyRows(form: VideoModelPricesForm, platform = 'grok') {
  if (platform === 'video-lt') {
    const families = videoPriceFamilies(platform)
    return [...families, ...Object.keys(form)
      .filter(key => !families.some(family => family.key === key) &&
        Object.values(form[key] ?? {}).some(value => normalizePrice(value) !== null))
      .sort().map(key => ({ key, label: key }))]
  }
  if (platform === 'bailian') {
    return [...bailianVideoPriceFamilies, ...Object.keys(form)
      .filter(key => !bailianVideoPriceFamilies.some(family => family.key === key) &&
        (!grokVideoPriceFamilies.some(family => family.key === key) ||
          Object.values(form[key] ?? {}).some(value => normalizePrice(value) !== null)))
      .sort().map(key => ({ key, label: key }))]
  }
  const known = new Set<string>(grokVideoPriceFamilies.map(({ key }) => key))
  const extra = Object.keys(form)
    .map(normalizeFamily)
    .filter((family) => family && !known.has(family))
    .sort()
    .map((key) => ({ key, label: key }))
  return [...grokVideoPriceFamilies, ...extra]
}
