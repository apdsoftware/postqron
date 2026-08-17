<script setup lang="ts">
import {
  apiBand,
  articles,
  blogIntro,
  features,
  featuresIntro,
  hero,
  plans,
  pricingIntro,
  showcases,
  stats,
  testimonials,
  testimonialsIntro,
} from '~/content/home'

const { public: config } = useRuntimeConfig()

useHead({
  title: 'Cronjob affidabili, definiti come codice',
  link: [
    { rel: 'canonical', href: canonicalUrl('/', config.siteUrl) },
    { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
  ],
  meta: [
    { name: 'description', content: hero.text },
    { property: 'og:type', content: 'website' },
    { property: 'og:title', content: `${hero.title} · PostQron` },
    { property: 'og:description', content: hero.text },
    { property: 'og:url', content: canonicalUrl('/', config.siteUrl) },
  ],
})

/** Le card entrano una dopo l'altra, 0,2s di scarto l'una dall'altra. */
function staggered(index: number) {
  return { direction: 'bottom' as const, distance: '50px', duration: 0.6, delay: 0.2 * (index + 1) }
}
</script>

<template>
  <div>
    <HomeHero v-bind="hero" />

    <PageSection id="funzionalita">
      <SectionHeading
        :title="featuresIntro.title"
        :lead="featuresIntro.lead"
      />
      <div class="row">
        <div
          v-for="(feature, index) in features"
          :key="feature.title"
          v-reveal="staggered(index)"
          class="col-lg-3 col-md-6 col-sm-6 col-12"
        >
          <FeatureCard v-bind="feature" />
        </div>
      </div>
    </PageSection>

    <PageSection
      white
      tight
      divider
      watermark="right"
    >
      <ShowcaseBlock v-bind="showcases[0]!" />
    </PageSection>

    <PageSection
      white
      tight
      watermark="left"
    >
      <ShowcaseBlock v-bind="showcases[1]!" />
    </PageSection>

    <GradientBand id="api">
      <div class="row">
        <div class="offset-lg-2 col-lg-8">
          <p class="api-band__text">
            {{ apiBand.text }}
          </p>
        </div>
      </div>
      <div class="row">
        <div
          v-for="channel in apiBand.channels"
          :key="channel.label"
          class="col-lg-4 col-md-4 col-sm-12 api-band__cell"
        >
          <NuxtLink
            :to="channel.to"
            class="api-band__button"
          >
            <HexIcon
              :name="channel.icon"
              class="api-band__icon"
            />
            <span>{{ channel.label }}</span>
          </NuxtLink>
        </div>
      </div>
    </GradientBand>

    <PageSection id="testimonianze">
      <SectionHeading
        :title="testimonialsIntro.title"
        :lead="testimonialsIntro.lead"
      />
      <div class="row">
        <div
          v-for="testimonial in testimonials"
          :key="testimonial.name"
          class="col-lg-4 col-md-6 col-sm-12"
        >
          <TestimonialCard v-bind="testimonial" />
        </div>
      </div>
    </PageSection>

    <PageSection
      id="prezzi"
      white
      watermark="left"
    >
      <SectionHeading
        :title="pricingIntro.title"
        :lead="pricingIntro.lead"
      />
      <div class="row">
        <div
          v-for="(plan, index) in plans"
          :key="plan.name"
          v-reveal="staggered(index)"
          class="col-lg-4 col-md-4 col-sm-12"
        >
          <PricingCard
            :plan="plan"
            :position="index + 1"
          />
        </div>
      </div>
    </PageSection>

    <GradientBand id="statistiche">
      <div class="row">
        <div
          v-for="stat in stats"
          :key="stat.label"
          class="col-lg-3 col-md-6 col-sm-12"
        >
          <StatCounter
            :value="stat.value"
            :label="stat.label"
          />
        </div>
      </div>
    </GradientBand>

    <PageSection
      id="blog"
      white
      watermark="left"
    >
      <SectionHeading
        :title="blogIntro.title"
        :lead="blogIntro.lead"
      />
      <div class="row">
        <div
          v-for="article in articles"
          :key="article.title"
          class="col-lg-4 col-md-6 col-sm-12"
        >
          <ArticleCard v-bind="article" />
        </div>
      </div>
    </PageSection>
  </div>
</template>

<style scoped>
.api-band__text {
  color: #fff;
  font-size: 22px;
  font-weight: 500;
  letter-spacing: 1.38px;
  line-height: 34px;
  text-align: center;
}

.api-band__cell {
  margin-top: 60px;
  text-align: center;
}

.api-band__button {
  display: inline-block;
  position: relative;
  width: 160px;
  height: 40px;
  overflow: hidden;
  border: 1px solid #fff;
  border-radius: var(--pq-radius-pill);

  /* Poco meno dell'altezza del pulsante: centra icona ed etichetta. */
  line-height: 38px;
  text-align: center;
}

.api-band__button::before {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 1;
  opacity: 0.3;
  background: #fff;
  transition: var(--pq-transition);
}

.api-band__button:hover::before {
  opacity: 1;
}

.api-band__icon {
  position: relative;
  z-index: 2;
  margin-right: 5px;
  color: #fff;
  transition: var(--pq-transition);
}

.api-band__button span {
  display: inline-block;
  position: relative;
  z-index: 2;
  color: #fff;
  font-size: 14px;
  font-weight: 700;
  letter-spacing: 0.88px;
  transition: var(--pq-transition);
}

.api-band__button:hover .api-band__icon,
.api-band__button:hover span {
  color: var(--pq-primary);
}

@media (max-width: 991px) {
  .api-band__cell {
    margin-top: 20px;
  }
}
</style>
