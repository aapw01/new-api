/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { TFunction } from 'i18next'
import { describe, expect, test, vi } from 'vitest'

import type { AttributionTrend } from '../../types'
import { buildAttributionTrendSpec } from '../attribution-chart'

describe('cost attribution trend chart', () => {
  test('maps every bucket and series while preserving zero values', () => {
    const trend: AttributionTrend = {
      buckets: [1_704_067_200, 1_704_153_600],
      series: [
        { key: 'gpt-4.1', label: 'GPT 4.1', points: [125, 0] },
        { key: 'claude', label: '', points: [40] },
      ],
    }
    const t = vi.fn((key: string) => key) as unknown as TFunction

    const spec = buildAttributionTrendSpec(trend, t)
    const values = spec.data[0].values

    expect(values).toHaveLength(4)
    expect(
      values.map((item: { Name: string; rawQuota: number }) => item.Name)
    ).toEqual(['GPT 4.1', 'claude', 'GPT 4.1', 'claude'])
    expect(
      values.map((item: { Name: string; rawQuota: number }) => item.rawQuota)
    ).toEqual([125, 40, 0, 0])
  })

  test('uses the localized empty label when a series has no identity', () => {
    const t = vi.fn(
      (key: string) => `translated:${key}`
    ) as unknown as TFunction

    const spec = buildAttributionTrendSpec(
      {
        buckets: [1_704_067_200],
        series: [{ key: '', label: '', points: [10] }],
      },
      t
    )

    expect(spec.data[0].values[0].Name).toBe('translated:(empty)')
    expect(t).toHaveBeenCalledWith('(empty)')
  })
})
