<script setup lang="ts">
import { useMarketingSiteI18n } from '../locales/runtime.ts'

const i18n = useMarketingSiteI18n()
const t = (key: string, params?: Record<string, string | number>) =>
  i18n.translate(`marketing-planner-preview.${key}`, params)
</script>

<template>
  <div
    class="planner-preview"
    :aria-label="t('aria.previewLabel')"
    role="img"
  >
    <div class="planner-preview__top">
      <span class="planner-preview__mark">P</span>
      <span>{{ t('month.label') }}</span>
      <span class="planner-preview__avatar">CM</span>
    </div>
    <div
      class="planner-preview__days"
      aria-hidden="true"
    >
      <span>{{ t('day.mon') }}<br><strong>20</strong></span>
      <span>{{ t('day.tue') }}<br><strong>21</strong></span>
      <span class="is-today">{{ t('day.wed') }}<br><strong>22</strong></span>
      <span>{{ t('day.thu') }}<br><strong>23</strong></span>
      <span>{{ t('day.fri') }}<br><strong>24</strong></span>
    </div>
    <div
      class="planner-preview__timeline"
      aria-hidden="true"
    >
      <span class="planner-preview__time">09:00</span>
      <article class="planner-card planner-card--green">
        <small>{{ t('card1.channels') }}</small>
        <strong>{{ t('card1.title') }}</strong>
        <span>{{ t('card.todayAt', { time: '09:30' }) }}</span>
      </article>
      <span class="planner-preview__time">14:00</span>
      <article class="planner-card planner-card--coral">
        <small>{{ t('card2.channels') }}</small>
        <strong>{{ t('card2.title') }}</strong>
        <span>{{ t('card.todayAt', { time: '14:15' }) }}</span>
      </article>
    </div>
  </div>
</template>
