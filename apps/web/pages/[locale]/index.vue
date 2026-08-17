<script setup lang="ts">
import { isLocaleCode } from '~/utils/locale'
import { interpolate } from '~/utils/site'

/**
 * Home, una volta per lingua.
 *
 * La lingua è un segmento della rotta e non uno stato dell'applicazione: la
 * pagina esiste pre-renderizzata sotto `/en/`, `/it/`, `/es/`, `/de/` e `/fr/`
 * (SPEC §8-bis). `validate` chiude la rotta alle cinque lingue: senza,
 * `/xx/` renderizzerebbe la home in inglese sotto un indirizzo che non esiste.
 */
definePageMeta({
  validate: route => isLocaleCode(route.params.locale),

  // Cambiare lingua è un cambio di parametro sulla stessa rotta: senza questa
  // chiave Vue riuserebbe l'istanza e `setup` non ripartirebbe, lasciando in
  // testa il `canonical` e il `lang` della lingua precedente.
  key: route => String(route.params.locale),
})

const { locale, content, href } = useSiteLocale()

useLocalizedHead({
  path: '/',
  locale: locale.value,
  title: content.value.meta.title,
  description: content.value.meta.description,
})

/** Le card entrano una dopo l'altra, 0,2s di scarto l'una dall'altra. */
function staggered(index: number) {
  return { direction: 'bottom' as const, distance: '50px', duration: 0.6, delay: 0.2 * (index + 1) }
}
</script>

<template>
  <div>
    <HomeHero
      v-bind="content.hero"
      :email-placeholder="content.ui.emailPlaceholder"
      :email-submit="content.ui.emailSubmit"
      :close-video-label="content.ui.closeVideo"
    />

    <PageSection id="features">
      <SectionHeading
        :title="content.featuresIntro.title"
        :lead="content.featuresIntro.lead"
      />
      <div class="row">
        <div
          v-for="(feature, index) in content.features"
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
      <ShowcaseBlock v-bind="content.showcases[0]!" />
    </PageSection>

    <PageSection
      white
      tight
      watermark="left"
    >
      <ShowcaseBlock v-bind="content.showcases[1]!" />
    </PageSection>

    <GradientBand id="api">
      <div class="row">
        <div class="offset-lg-2 col-lg-8">
          <p class="api-band__text">
            {{ content.apiBand.text }}
          </p>
        </div>
      </div>
      <div class="row">
        <div
          v-for="channel in content.apiBand.channels"
          :key="channel.label"
          class="col-lg-4 col-md-4 col-sm-12 api-band__cell"
        >
          <NuxtLink
            :to="href(channel.to)"
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

    <PageSection id="testimonials">
      <SectionHeading
        :title="content.testimonialsIntro.title"
        :lead="content.testimonialsIntro.lead"
      />
      <div class="row">
        <div
          v-for="testimonial in content.testimonials"
          :key="testimonial.name"
          class="col-lg-4 col-md-6 col-sm-12"
        >
          <TestimonialCard
            v-bind="testimonial"
            :photo-alt="interpolate(content.ui.photoOf, { name: testimonial.name })"
          />
        </div>
      </div>
    </PageSection>

    <!--
      Quattro piani su dodici colonne: SPEC §8 ne definisce quattro pubblici e
      acquistabili, e mostrarne tre presenterebbe come completa un'offerta che
      non lo è. La griglia del tema ne prevedeva tre da 416px; qui le card
      passano a 306px, la stessa misura delle card delle funzionalità qui sopra,
      quindi la larghezza resta una di quelle già verificate contro il tema.
    -->
    <PageSection
      id="pricing"
      white
      watermark="left"
    >
      <SectionHeading
        :title="content.pricingIntro.title"
        :lead="content.pricingIntro.lead"
      />
      <div class="row">
        <div
          v-for="(plan, index) in content.plans"
          :key="plan.name"
          v-reveal="staggered(index)"
          class="col-lg-3 col-md-6 col-sm-12"
        >
          <PricingCard
            :plan="plan"
            :position="index + 1"
            :href="href(plan.ctaTo)"
            :currency-position="content.money.currencyPosition"
            :tax-note="content.money.taxNote"
          />
        </div>
      </div>
    </PageSection>

    <GradientBand id="stats">
      <div class="row">
        <div
          v-for="stat in content.stats"
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
        :title="content.blogIntro.title"
        :lead="content.blogIntro.lead"
      />
      <div class="row">
        <div
          v-for="article in content.articles"
          :key="article.title"
          class="col-lg-4 col-md-6 col-sm-12"
        >
          <ArticleCard
            v-bind="article"
            :to="href(article.to)"
            :cta-label="content.ui.readMore"
          />
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
