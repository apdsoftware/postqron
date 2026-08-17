<script setup lang="ts">
import type { LocaleCode } from '~/utils/locale'
import { LOCALES, localePath, stripLocale } from '~/utils/locale'

/** Scorrimento oltre il quale l'header diventa una barra bianca compatta. */
const STICKY_OFFSET = 30

const route = useRoute()
const { locale, content, href } = useSiteLocale()

const isScrolled = useScrolledPast(STICKY_OFFSET)
const isMenuOpen = ref(false)
/** Indice del sottomenu aperto sotto i 992px, dove la tendina è a scatto. */
const openSubmenu = ref<number | null>(null)

/**
 * Il selettore di lingua è l'ultima tendina della barra e ne condivide stato e
 * stile: sotto i 992px si apre a scatto come le altre, sopra al passaggio del
 * mouse. L'indice viene dopo quelli del menu, che è esattamente il posto che
 * occupa nel markup.
 */
const languageSubmenuIndex = computed(() => content.value.nav.main.length)

const currentLanguage = computed(
  () => LOCALES.find(entry => entry.code === locale.value) ?? LOCALES[0]!,
)

/**
 * Indirizzo della pagina corrente in un'altra lingua.
 *
 * Si ricava dal percorso senza prefisso, non da una tabella di corrispondenze:
 * la struttura delle rotte è la stessa in tutte e cinque le lingue, quindi
 * `/it/prezzi/` è `/es/prezzi/` finché i percorsi non verranno tradotti.
 */
function localeHref(code: LocaleCode) {
  return localePath(stripLocale(route.path), code)
}

function closeMenu() {
  isMenuOpen.value = false
  openSubmenu.value = null
}

function toggleSubmenu(index: number) {
  openSubmenu.value = openSubmenu.value === index ? null : index
}

/**
 * R32: la scelta esplicita prevale sul rilevamento e persiste fra le visite.
 * La navigazione la fa il `NuxtLink`; qui si registra soltanto che è stata una
 * scelta e non un rilevamento.
 */
function chooseLocale(code: LocaleCode) {
  rememberLocale(code)
  closeMenu()
}
</script>

<template>
  <header
    class="site-header"
    :class="{ 'is-scrolled': isScrolled }"
  >
    <div class="container">
      <nav class="site-header__nav">
        <NuxtLink
          :to="href('/')"
          class="site-header__logo"
          :aria-label="content.ui.homeLink"
        >
          <SiteLogo :height="37" />
        </NuxtLink>

        <button
          type="button"
          class="site-header__trigger"
          :class="{ 'is-active': isMenuOpen }"
          :aria-expanded="isMenuOpen"
          aria-controls="site-menu"
          @click="isMenuOpen = !isMenuOpen"
        >
          <span class="site-header__bars" />
          <span class="visually-hidden">{{ content.ui.menu }}</span>
        </button>

        <ul
          id="site-menu"
          class="site-header__menu"
          :class="{ 'is-open': isMenuOpen }"
        >
          <li
            v-for="(item, index) in content.nav.main"
            :key="item.label"
            :class="{ 'has-submenu': item.children, 'is-expanded': openSubmenu === index }"
          >
            <NuxtLink
              v-if="item.to"
              :to="href(item.to)"
              @click="closeMenu"
            >
              {{ item.label }}
            </NuxtLink>

            <!--
              Sopra i 992px la tendina si apre al passaggio del mouse, via CSS.
              Sotto, dove il passaggio del mouse non esiste, lo stesso elemento
              diventa un pulsante che apre e chiude: un markup solo, due
              comportamenti, e la tastiera funziona in entrambi i casi.
            -->
            <template v-else-if="item.children">
              <button
                type="button"
                :aria-expanded="openSubmenu === index"
                @click="toggleSubmenu(index)"
              >
                {{ item.label }}
                <span class="site-header__caret"><HexIcon name="angleDown" /></span>
              </button>
              <ul class="site-header__submenu">
                <li
                  v-for="child in item.children"
                  :key="child.label"
                >
                  <NuxtLink
                    :to="href(child.to!)"
                    @click="closeMenu"
                  >{{ child.label }}</NuxtLink>
                </li>
              </ul>
            </template>
          </li>

          <!--
            Selettore di lingua (R32). Le voci non sono tradotte: ogni lingua si
            chiama con il proprio nome, che è la sola forma riconoscibile da chi
            non capisce quella corrente.
          -->
          <li
            class="has-submenu"
            :class="{ 'is-expanded': openSubmenu === languageSubmenuIndex }"
          >
            <button
              type="button"
              :aria-expanded="openSubmenu === languageSubmenuIndex"
              @click="toggleSubmenu(languageSubmenuIndex)"
            >
              <span class="visually-hidden">{{ content.ui.language }}: </span>
              <span :lang="currentLanguage.htmlLang">{{ currentLanguage.label }}</span>
              <span class="site-header__caret"><HexIcon name="angleDown" /></span>
            </button>
            <ul class="site-header__submenu">
              <li
                v-for="entry in LOCALES"
                :key="entry.code"
              >
                <NuxtLink
                  :to="localeHref(entry.code)"
                  :hreflang="entry.htmlLang"
                  :lang="entry.htmlLang"
                  :aria-current="entry.code === locale ? 'true' : undefined"
                  @click="chooseLocale(entry.code)"
                >{{ entry.label }}</NuxtLink>
              </li>
            </ul>
          </li>

          <li>
            <NuxtLink
              :to="href(content.nav.cta.to)"
              class="site-header__cta"
              @click="closeMenu"
            >
              <span>{{ content.nav.cta.label }}</span>
            </NuxtLink>
          </li>
        </ul>
      </nav>
    </div>
  </header>
</template>

<style scoped>
.site-header {
  position: fixed;
  top: 0;
  right: 0;
  left: 0;
  height: var(--pq-header-height);
  z-index: 100;
  transition: var(--pq-transition);
}

.site-header.is-scrolled {
  height: var(--pq-header-height-compact);
  background: var(--pq-surface);
  box-shadow: var(--pq-shadow-header);
}

.site-header__logo {
  float: left;
  margin-top: 30px;
  transition: var(--pq-transition);
}

.site-header.is-scrolled .site-header__logo {
  margin-top: 22px;
}

.site-header__menu {
  display: flex;
  float: right;
  margin-top: 27px;
  transition: var(--pq-transition);
}

.site-header.is-scrolled .site-header__menu {
  margin-top: 21px;
}

.site-header__menu > li {
  padding-right: 20px;
  padding-left: 20px;
}

.site-header__menu > li:last-child {
  padding-right: 0;
}

.site-header__menu a,
.site-header__menu button {
  display: block;
  height: 40px;
  padding: 0;
  border: none;
  background: none;
  color: #fff;
  font-family: inherit;
  font-size: 14px;
  font-weight: 500;
  letter-spacing: 1px;
  line-height: 40px;
  cursor: pointer;
  transition: var(--pq-transition);
}

.site-header__menu a:hover,
.site-header__menu button:hover {
  color: var(--pq-text-on-gradient);
}

.site-header.is-scrolled .site-header__menu a,
.site-header.is-scrolled .site-header__menu button {
  color: var(--pq-heading);
}

.site-header.is-scrolled .site-header__menu a:hover,
.site-header.is-scrolled .site-header__menu button:hover {
  color: var(--pq-primary);
}

/* Invito all'azione: pillola dal bordo bianco con un velo interno. */
.site-header__menu a.site-header__cta {
  position: relative;
  height: 30px;
  margin-top: 5px;
  padding-right: 25px;
  padding-left: 25px;
  overflow: hidden;
  border: 1px solid #fff;
  border-radius: var(--pq-radius-pill);
  letter-spacing: 0.5px;
  line-height: 29px;
}

.site-header__menu a.site-header__cta::before {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 1;
  opacity: 0.19;
  background: #fff;
}

.site-header__cta span {
  position: relative;
  z-index: 2;
}

.site-header__menu a.site-header__cta:hover {
  background: #fff;
  color: var(--pq-primary);
}

.site-header.is-scrolled .site-header__menu a.site-header__cta {
  border-color: var(--pq-heading);
}

.site-header.is-scrolled .site-header__menu a.site-header__cta:hover {
  border-color: var(--pq-primary);
  background: var(--pq-primary);
  color: #fff;
}

/*
 * Il selettore ripete `.site-header__menu > li` perché deve superare in
 * specificità la spaziatura comune delle voci, non solo seguirla.
 */
.site-header__menu > li.has-submenu {
  position: relative;
  padding-right: 35px;
}

.site-header__caret {
  position: absolute;
  top: 12px;
  right: 18px;
  font-size: 12px;
  line-height: 1.5;
}

.site-header__submenu {
  position: absolute;
  top: 40px;
  width: 200px;
  z-index: -1;
  overflow: hidden;
  border-radius: var(--pq-radius);
  box-shadow: var(--pq-shadow-header);
  opacity: 0;
  visibility: hidden;
  transform: translateY(-2em);
  transition:
    all 0.3s ease-in-out 0s,
    visibility 0s linear 0.3s,
    z-index 0s linear 0.01s;
}

.has-submenu:hover .site-header__submenu,
.has-submenu:focus-within .site-header__submenu {
  z-index: 1;
  opacity: 1;
  visibility: visible;
  transform: translateY(0);
  transition-delay: 0s, 0s, 0.3s;
}

.site-header__submenu li {
  padding: 0;
}

.site-header__menu .site-header__submenu a {
  position: relative;
  height: 40px;
  padding-left: 20px;
  background: var(--pq-surface);
  color: var(--pq-heading);
  font-size: 13px;
  line-height: 40px;
}

/* Filo blu che entra da sinistra quando la voce è sotto il puntatore. */
.site-header__submenu a::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 0;
  background: var(--pq-primary);
  transition: var(--pq-transition);
}

.site-header__menu .site-header__submenu a:hover {
  padding-left: 25px;
  background: var(--pq-border-hairline);
}

.site-header__submenu a:hover::before {
  width: 3px;
}

/* Panino: tre tratti che al tocco diventano una croce. */
.site-header__trigger {
  display: none;
  position: absolute;
  top: 23px;
  right: 40px;
  width: 32px;
  height: 40px;
  padding: 0;
  z-index: 99;
  border: none;
  background: none;
  cursor: pointer;
}

.site-header__bars,
.site-header__bars::before,
.site-header__bars::after {
  position: absolute;
  left: 0;
  width: 30px;
  height: 2px;
  background-color: var(--pq-heading);
  transition: all 0.4s;
}

.site-header__bars {
  top: 16px;
}

.site-header__bars::before,
.site-header__bars::after {
  content: '';
  width: 75%;
}

.site-header__bars::before {
  top: -10px;
  z-index: 10;
  transform-origin: 33% 100%;
}

.site-header__bars::after {
  top: 10px;
  transform-origin: 33% 0;
}

.is-active .site-header__bars {
  background-color: transparent;
}

.is-active .site-header__bars::before,
.is-active .site-header__bars::after {
  width: 100%;
}

.is-active .site-header__bars::before {
  transform: translateY(6px) translateX(1px) rotate(45deg);
}

.is-active .site-header__bars::after {
  transform: translateY(-6px) translateX(1px) rotate(-45deg);
}

@media (max-width: 1200px) {
  .site-header__menu > li {
    padding-right: 12px;
    padding-left: 12px;
  }

  .site-header__menu > li.has-submenu {
    padding-right: 20px;
  }

  .site-header__caret {
    right: 5px;
  }
}

@media (max-width: 991px) {
  .site-header,
  .site-header.is-scrolled {
    height: var(--pq-header-height-compact);
    background: var(--pq-surface);
    box-shadow: var(--pq-shadow-header);
  }

  .site-header .container {
    padding: 0;
  }

  .site-header__nav {
    overflow: hidden;
  }

  .site-header__logo,
  .site-header.is-scrolled .site-header__logo {
    margin-top: 22px;
    margin-left: 30px;
  }

  .site-header__trigger {
    display: block;
  }

  .site-header__menu,
  .site-header.is-scrolled .site-header__menu {
    display: none;
    float: none;
    width: 100%;
    margin-top: 80px;
  }

  .site-header__menu.is-open {
    display: block;
  }

  .site-header__menu > li {
    width: 100%;
    padding-right: 0;
    padding-left: 0;
    border-bottom: 1px solid var(--pq-border-hairline);
    background: var(--pq-surface);
  }

  .site-header__menu a,
  .site-header__menu button,
  .site-header.is-scrolled .site-header__menu a,
  .site-header.is-scrolled .site-header__menu button,
  .site-header__menu a.site-header__cta,
  .site-header.is-scrolled .site-header__menu a.site-header__cta {
    width: 100%;
    height: 50px;
    margin-top: 0;
    padding: 0 0 0 30px;
    border: none;
    border-radius: 0;
    background: var(--pq-surface);
    color: var(--pq-heading);
    line-height: 50px;
    text-align: left;
  }

  .site-header__menu a:hover,
  .site-header__menu button:hover,
  .site-header.is-scrolled .site-header__menu a:hover,
  .site-header.is-scrolled .site-header__menu button:hover {
    background: var(--pq-border-hairline);
    color: var(--pq-heading);
  }

  .site-header__menu a.site-header__cta::before {
    content: none;
  }

  .site-header__caret {
    top: 15px;
    right: 25px;
    font-size: 14px;
  }

  /*
   * La tendina non si apre più al passaggio del mouse ma allo scatto: resta
   * nel flusso e passa da altezza zero a quella del contenuto.
   */
  .site-header__submenu,
  .has-submenu:hover .site-header__submenu,
  .has-submenu:focus-within .site-header__submenu {
    position: relative;
    top: 0;
    width: 100%;
    height: 0;
    z-index: 1;
    box-shadow: none;
    opacity: 1;
    visibility: inherit;
    transform: none;
  }

  .is-expanded .site-header__submenu,
  .has-submenu.is-expanded:hover .site-header__submenu {
    height: auto;
  }

  .site-header__menu .site-header__submenu a {
    padding-left: 50px;
  }

  .site-header__menu .site-header__submenu a:hover {
    padding-left: 50px;
  }

  .site-header__submenu a:hover::before {
    width: 0;
  }
}
</style>
