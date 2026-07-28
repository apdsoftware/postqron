export const CHANNEL_SLIDER_MINIMUM = 1

export function sliderMarkerPosition(
  value: number,
  minimum: number,
  maximum: number,
): string {
  if (
    !Number.isFinite(value)
    || !Number.isFinite(minimum)
    || !Number.isFinite(maximum)
    || maximum <= minimum
    || value < minimum
    || value > maximum
  ) {
    throw new RangeError('INVALID_SLIDER_MARKER')
  }

  return `${((value - minimum) / (maximum - minimum)) * 100}%`
}
