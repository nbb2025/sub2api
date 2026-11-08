import { describe, expect, it } from 'vitest'

import {
  createVideoModelPricesForm,
  serializeVideoModelPrices,
  videoModelPriceFamilyRows
} from '../groupsVideoModelPricing'

describe('Grok video model pricing form', () => {
  it('keeps Wan standard and prime rates independent with eight decimal places', () => {
    const form = createVideoModelPricesForm({ 'wan-friendly': { '720p': 0.01234567 } }, 'bailian')
    form['wan3.0-video']['1080p'] = 0.02345678
    form['wan3.0-video-prime']['1080p'] = 0.12345678
    expect(videoModelPriceFamilyRows(form, 'bailian').map(({ key }) => key)).toEqual([
      'wan3.0-video', 'wan3.0-video-prime', 'wan-friendly'
    ])
    expect(serializeVideoModelPrices(form)).toEqual({
      'wan-friendly': { '720p': 0.01234567 },
      'wan3.0-video': { '1080p': 0.02345678 },
      'wan3.0-video-prime': { '1080p': 0.12345678 }
    })
  })
  it('provides editable rows for both canonical Grok video families', () => {
    const form = createVideoModelPricesForm()

    expect(videoModelPriceFamilyRows(form).map(({ key }) => key)).toEqual([
      'grok-imagine-video',
      'grok-imagine-video-1.5'
    ])
    expect(form['grok-imagine-video']['480p']).toBeNull()
    expect(form['grok-imagine-video-1.5']['1080p']).toBeNull()
  })

  it('serializes only finite non-negative prices and preserves future families', () => {
    const form = createVideoModelPricesForm({
      'grok-imagine-video-2': { '1080p': 0.4 }
    })
    form['grok-imagine-video']['480p'] = 0.05
    form['grok-imagine-video']['720p'] = ''
    form['grok-imagine-video-1.5']['1080p'] = -1

    expect(serializeVideoModelPrices(form)).toEqual({
      'grok-imagine-video': { '480p': 0.05 },
      'grok-imagine-video-2': { '1080p': 0.4 }
    })
  })

  it('round-trips unknown model families so editing does not discard them', () => {
    const form = createVideoModelPricesForm({
      'grok-imagine-video-2': { '480p': 0.2 }
    })

    expect(videoModelPriceFamilyRows(form).map(({ key }) => key)).toContain(
      'grok-imagine-video-2'
    )
    expect(serializeVideoModelPrices(form)).toMatchObject({
      'grok-imagine-video-2': { '480p': 0.2 }
    })
  })
})
