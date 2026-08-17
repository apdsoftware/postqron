<script setup lang="ts">
/*
 * Le etichette non hanno valori predefiniti: un default è una stringa scritta
 * nel componente, e una stringa scritta nel componente non è traducibile
 * (SPEC §8-bis). Arrivano da `content/<lingua>.ts`, sezione `ui`.
 */
withDefaults(
  defineProps<{
    placeholder: string
    submitLabel: string
    /** Nota sotto al campo, in caratteri piccoli. */
    note?: string
  }>(),
  { note: undefined },
)

const email = defineModel<string>({ default: '' })

const emit = defineEmits<{ submit: [email: string] }>()
</script>

<template>
  <form
    class="email-signup"
    @submit.prevent="emit('submit', email)"
  >
    <div class="email-signup__field">
      <label
        class="visually-hidden"
        for="email-signup"
      >{{ placeholder }}</label>
      <input
        id="email-signup"
        v-model="email"
        type="email"
        name="email"
        autocomplete="email"
        required
        :placeholder="placeholder"
      >
      <button type="submit">
        {{ submitLabel }}
      </button>
    </div>
    <span
      v-if="note"
      class="email-signup__note"
    >{{ note }}</span>
  </form>
</template>

<style scoped>
.email-signup {
  position: relative;
  width: 80%;
  overflow: hidden;
}

/*
 * Campo e pulsante sono in posizione assoluta, come nel tema: il pulsante si
 * innesta nella pillola del campo invece di affiancarla.
 */
.email-signup__field input {
  position: absolute;
  width: 100%;
  height: 46px;
  z-index: 1;
  padding-right: 120px;
  padding-left: 20px;
  border: 1px solid var(--pq-border-input);
  border-radius: var(--pq-radius-pill);
  outline: none;
  color: var(--pq-text);
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.67px;
  transition: var(--pq-transition);
}

.email-signup__field input::placeholder {
  color: var(--pq-text);
}

.email-signup__field input:focus {
  padding-left: 30px;
}

/*
 * Il tema azzera il contorno di messa a fuoco su campo e pulsante: chi naviga
 * da tastiera perderebbe di vista dov'è. Al posto del contorno di sistema, che
 * squadrerebbe la pillola, il bordo diventa blu e si aggiunge un alone.
 */
.email-signup__field input:focus-visible {
  border-color: var(--pq-primary);
  box-shadow: 0 0 0 3px rgb(66 120 229 / 25%);
}

.email-signup__field button:focus-visible {
  box-shadow: 0 0 0 3px rgb(66 120 229 / 35%);
}

.email-signup__field button {
  position: absolute;
  right: 0;
  width: 98px;
  height: 46px;
  z-index: 2;
  border: none;
  border-radius: 0 var(--pq-radius-pill) var(--pq-radius-pill) 0;
  background: var(--pq-primary);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  outline: none;
  cursor: pointer;
}

.email-signup__note {
  display: block;
  margin-top: 54px;
  padding-left: 5px;
  color: var(--pq-text);
  font-size: 12px;
  letter-spacing: 0.67px;
}

@media (max-width: 1200px) {
  .email-signup {
    width: 100%;
  }
}

@media (max-width: 991px) {
  .email-signup__note {
    color: #fff;
    text-align: center;
  }
}
</style>
