<script setup lang="ts">
/**
 * Guscio dell'applicazione.
 *
 * Per ora contiene solo il selettore di lingua: il layout Flowbite completo —
 * barra laterale, navigazione, menu utente — arriva con la issue della dashboard
 * cliente (backlog 24). Il selettore sta qui e non nella pagina perché R32 lo
 * vuole «nell'interfaccia», cioè su ogni schermata: metterlo in una pagina sola
 * significherebbe che dalle altre non si può cambiare lingua.
 */
</script>

<template>
  <div class="shell">
    <header class="shell__bar">
      <LocaleSwitcher />
    </header>

    <slot />
  </div>
</template>

<style scoped>
.shell {
  min-height: 100vh;
  font-family: system-ui, sans-serif;
  line-height: 1.6;
}

.shell__bar {
  display: flex;
  justify-content: flex-end;
  gap: 1rem;
  border-bottom: 1px solid #e4e4e7;
  padding: 0.75rem 1.5rem;
}
</style>
