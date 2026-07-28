import assert from 'node:assert/strict'
import test from 'node:test'
import { sliderMarkerPosition } from '../src/slider-geometry.ts'

test('slider markers follow their values across the useful thumb path', () => {
  assert.deepEqual(
    [1, 3, 6, 9, 10].map(value => sliderMarkerPosition(value, 1, 10)),
    ['0%', '22.22222222222222%', '55.55555555555556%', '88.88888888888889%', '100%'],
  )
})

test('slider marker geometry validates the range and marker value', () => {
  assert.throws(() => sliderMarkerPosition(1, 1, 1), /INVALID_SLIDER_MARKER/u)
  assert.throws(() => sliderMarkerPosition(0, 1, 10), /INVALID_SLIDER_MARKER/u)
  assert.throws(() => sliderMarkerPosition(11, 1, 10), /INVALID_SLIDER_MARKER/u)
})
