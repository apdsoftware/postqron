<script setup lang="ts">
/**
 * Interruttore fra tema chiaro e scuro (template: `#theme-toggle`).
 *
 * Il template mostra le due icone insieme e ne nasconde una con `classList`;
 * qui ce n'è una sola alla volta, perché lo stato è noto a Vue e un elemento in
 * meno nel DOM è un elemento in meno che un lettore di schermo deve saltare.
 *
 * L'etichetta dice **dove porta** e non dove si è: su un'interfaccia già scura,
 * «tema scuro» si leggerebbe come lo stato corrente e il pulsante sembrerebbe
 * non fare niente.
 */
const { isDark, toggle } = useColorScheme()
const { t } = useLocale()
</script>

<template>
  <button
    type="button"
    class="p-2.5 text-sm text-gray-500 rounded-lg dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-700 focus:outline-none focus:ring-4 focus:ring-gray-200 dark:focus:ring-gray-700"
    :aria-label="isDark ? t.shell.toLightTheme : t.shell.toDarkTheme"
    data-testid="theme-toggle"
    @click="toggle"
  >
    <AppIcon
      :name="isDark ? 'sun' : 'moon'"
      class="w-5 h-5"
    />
  </button>
</template>
