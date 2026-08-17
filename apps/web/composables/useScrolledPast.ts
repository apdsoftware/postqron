/**
 * Segnala quando la pagina ha superato una certa soglia di scorrimento.
 *
 * È il rimpiazzo di `scrollNavBar()` di custom.js. Parte da `false`, che è lo
 * stato corretto dell'HTML pre-renderizzato: la pagina si apre sempre in cima.
 *
 * @param offset soglia in pixel
 */
export function useScrolledPast(offset: number) {
  const isPast = ref(false)

  onMounted(() => {
    const update = () => {
      isPast.value = window.scrollY >= offset
    }

    // Al ricaricamento il browser può ripristinare una posizione già scorsa.
    update()
    window.addEventListener('scroll', update, { passive: true })
    onBeforeUnmount(() => window.removeEventListener('scroll', update))
  })

  return readonly(isPast)
}
