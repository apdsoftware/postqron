<script setup lang="ts">
withDefaults(
  defineProps<{
    /** Titolo della sezione. */
    title: string
    /** Occhiello sotto il titolo, largo metà pagina e centrato. */
    lead?: string
    /** Su fondo gradiente titolo e testo passano ai toni chiari. */
    onGradient?: boolean
  }>(),
  { lead: undefined },
)
</script>

<template>
  <div class="row">
    <div class="col-lg-12">
      <h2
        class="section-heading__title"
        :class="{ 'is-on-gradient': onGradient }"
      >
        {{ title }}
      </h2>
    </div>
    <div
      v-if="lead"
      class="offset-lg-3 col-lg-6"
    >
      <div
        class="section-heading__lead"
        :class="{ 'is-on-gradient': onGradient }"
      >
        <p>{{ lead }}</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.section-heading__title {
  margin-bottom: 20px;
  color: var(--pq-heading);
  font-size: 28px;
  font-weight: 400;
  letter-spacing: 1.75px;
  line-height: 38px;
  text-align: center;
}

.section-heading__lead {
  margin-bottom: 50px;
  color: var(--pq-text);
  font-size: 16px;
  letter-spacing: 1px;
  line-height: 28px;
  text-align: center;
}

.section-heading__lead p {
  margin-bottom: 30px;
}

.is-on-gradient.section-heading__title {
  color: #fff;
}

.is-on-gradient.section-heading__lead {
  color: var(--pq-text-on-gradient);
}
</style>
