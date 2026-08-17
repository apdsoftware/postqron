<script setup lang="ts">
import { socialLinks } from '~/content/social'

const { content, href } = useSiteLocale()

const year = new Date().getFullYear()
</script>

<template>
  <footer
    id="contact"
    class="site-footer"
  >
    <div class="container">
      <div class="row">
        <div class="col-lg-5 col-md-12 col-sm-12">
          <SiteLogo
            :height="37"
            class="site-footer__logo"
          />
          <p class="site-footer__about">
            {{ content.company.about }}
          </p>
        </div>

        <div
          v-for="group in content.nav.footer"
          :key="group.title"
          class="col-lg-2 col-md-4 col-sm-6 col-6"
        >
          <h2 class="site-footer__title">
            {{ group.title }}
          </h2>
          <ul class="site-footer__nav">
            <li
              v-for="item in group.items"
              :key="item.label"
            >
              <NuxtLink :to="href(item.to!)">
                <span class="site-footer__nav-icon"><HexIcon name="angleRight" /></span>
                <span class="site-footer__nav-label">{{ item.label }}</span>
              </NuxtLink>
            </li>
          </ul>
        </div>

        <div class="col-lg-3 col-md-4 col-sm-12">
          <h2 class="site-footer__title">
            {{ content.ui.contactTitle }}
          </h2>
          <address class="site-footer__address">
            <p>{{ content.company.legalName }}<br>{{ content.company.address }}</p>
            <p>
              <span>{{ content.ui.emailPrefix }}</span>
              <a :href="`mailto:${content.company.email}`">{{ content.company.email }}</a>
            </p>
            <ul class="site-footer__social">
              <li
                v-for="link in socialLinks"
                :key="link.label"
              >
                <a
                  :href="link.href"
                  target="_blank"
                  rel="noopener"
                >
                  <HexIcon
                    :name="link.icon"
                    :label="link.label"
                  />
                </a>
              </li>
            </ul>
          </address>
        </div>
      </div>

      <div class="row">
        <div class="col-lg-12">
          <p class="site-footer__copyright">
            © {{ year }} {{ content.company.legalName }}. {{ content.ui.rightsReserved }}
          </p>
        </div>
      </div>
    </div>
  </footer>
</template>

<style scoped>
.site-footer {
  padding-top: 70px;
  background: var(--pq-surface-tint);
}

.site-footer__logo {
  margin-bottom: 30px;
}

.site-footer__about {
  color: var(--pq-text);
  font-size: 14px;
  letter-spacing: 0.88px;
  line-height: 26px;
}

.site-footer__title {
  margin-bottom: 30px;
  color: var(--pq-heading);
  font-size: 16px;
  font-weight: 400;
  letter-spacing: 0.69px;
  line-height: 30px;
}

.site-footer__nav a {
  display: block;
  overflow: hidden;
}

.site-footer__nav-icon {
  float: left;
  height: 32px;
  margin-right: 12px;
  color: var(--pq-heading);
  font-size: 14px;
  line-height: 32px;
}

.site-footer__nav-label {
  float: left;
  height: 32px;
  color: var(--pq-text);
  font-size: 14px;
  line-height: 32px;
  transition: var(--pq-transition);
}

.site-footer__nav a:hover .site-footer__nav-label {
  color: var(--pq-primary);
}

.site-footer__address {
  font-style: normal;
}

.site-footer__address p {
  display: block;
  margin-bottom: 5px;
  overflow: hidden;
  color: var(--pq-text);
  font-size: 14px;
  letter-spacing: 0.88px;
  line-height: 26px;
}

.site-footer__address a {
  color: var(--pq-primary);
}

.site-footer__social {
  display: flex;
  gap: 10px;
  margin-top: 5px;
  font-size: 16px;
}

.site-footer__social a {
  color: var(--pq-text);
  transition: var(--pq-transition);
}

.site-footer__social a:hover {
  color: var(--pq-primary);
}

.site-footer__copyright {
  margin-top: 30px;
  padding-top: 30px;
  padding-bottom: 30px;
  border-top: 1px solid var(--pq-border-footer);
  color: var(--pq-text);
  font-size: 14px;
  letter-spacing: 0.88px;
  text-align: center;
}

@media (max-width: 991px) {
  .site-footer__about {
    margin-bottom: 30px;
  }

  .site-footer__title {
    margin-bottom: 15px;
  }

  .site-footer__nav {
    margin-bottom: 30px;
  }
}
</style>
