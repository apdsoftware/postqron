---
document: acceptable-use-policy
version: 1.0.0
effective_date: 2026-08-17
language: es
status: pending-review
---

# Política de uso aceptable

Esta política forma parte de los [Términos del servicio](terms-of-service.md). Describe
qué no puedes hacer con Postqron, y qué ocurre cuando lo haces.

Postqron envía peticiones HTTP a las direcciones que elijas, según la programación que
elijas, desde nuestra infraestructura y nuestras direcciones IP. Esa capacidad es útil, y
es también la capacidad que quiere un atacante. Esta política existe para que la
diferencia entre ambas cosas esté escrita en lugar de quedar al criterio de alguien.

## 1. A quién se aplica

A todo el que use Postqron, en cualquier plan, incluido el gratuito. Se aplica también a
cualquiera al que invites a tu espacio de trabajo: eres responsable del uso que haga del
servicio.

## 2. Qué no debes hacer

### 2.1 Atacar, sobrecargar o sondear sistemas

No debes usar Postqron para:

- enviar peticiones a un sistema que no te pertenezca o que no estés expresamente
  autorizado a probar;
- generar carga con la intención de degradar, agotar o denegar el servicio de cualquier
  sistema, incluso mediante programaciones de alta frecuencia, muchos trabajos contra un
  único objetivo, o el uso coordinado de varias cuentas;
- escanear, enumerar o sondear hosts, puertos, rutas o credenciales;
- alcanzar sistemas que no están pensados para ser accesibles públicamente, incluidas
  redes privadas, direcciones de loopback, puntos de metadatos de la nube y servicios
  internos — nuestros o de cualquier otro.

La autorización importa más que la intención. Programar una petición contra el endpoint de
un tercero no se vuelve aceptable por llamarlo comprobación de estado.

### 2.2 Eludir nuestros controles

No debes intentar sortear las medidas técnicas que hacen cumplir esta política, incluidos
el filtrado de direcciones, los límites de frecuencia, los límites de plan o los techos de
ejecución. Esto incluye usar redirecciones, entradas DNS bajo tu control o proxies para
alcanzar un destino que de otro modo rechazaríamos.

### 2.3 Usar el servicio de forma ilícita o abusiva

No debes usar Postqron para infringir la ley, para vulnerar los derechos de alguien, para
distribuir malware, para enviar mensajes no solicitados, o para tratar contenidos ilícitos
en las jurisdicciones en las que estés tú o estén tus destinatarios.

### 2.4 Falsear el origen

No debes presentar peticiones procedentes de Postqron como si vinieran de otra persona, ni
usar el servicio para ocultar el origen de una actividad.

### 2.5 Revender o exponer el servicio como propio

No debes ofrecer a terceros la capacidad de ejecución de Postqron como un servicio tuyo
sin un acuerdo por escrito. Ejecutar trabajos por cuenta de tus propios clientes dentro de
un espacio de trabajo Agency es esperable y está permitido; construir un producto sobre
nuestro planificador y venderlo, no.

## 3. Recursos compartidos

Las peticiones salientes parten de direcciones IP compartidas por todos los clientes,
salvo cuando un plan incluya una dirección dedicada. La reputación de esas direcciones es
un bien común: el abuso de un cliente degrada el servicio para todos. Hacemos cumplir esta
política para proteger a los demás clientes, no para vigilarte.

Podemos aplicar límites agregados por host de destino, y podemos rechazar o ralentizar
peticiones hacia un destino que muestre señales de estar siendo atacado en lugar de
atendido.

## 4. Qué hacemos ante las infracciones

Cuando la situación lo permite, te contactamos primero y te damos la oportunidad de
corregirlo. Cuando no lo permite — porque el daño está en curso, porque un tercero está
siendo atacado, o porque la ley nos obliga a actuar — podemos actuar de inmediato y
decírtelo después.

Según la gravedad podemos:

1. **limitar o bloquear** trabajos o destinos concretos;
2. **suspender** los trabajos afectados dejando la cuenta por lo demás utilizable;
3. **suspender la cuenta**, deteniendo toda ejecución;
4. **cerrar** la cuenta.

Suspendemos lo más acotado que detiene el daño. La suspensión no es un supuesto de
reembolso: consulta los Términos.

Cuando suspendemos o cerramos, conservas el derecho a exportar tus datos
durante 30 días,
salvo que hacerlo sea ilícito.

## 5. Denunciar un abuso

Si crees que alguien está usando Postqron para atacar o abusar de un sistema del que eres
responsable, escribe a
abuse@postqron.com.
Incluye la dirección de destino, las marcas de tiempo en UTC y, si está disponible, la IP
de origen. Investigamos las denuncias y confirmaremos su recepción
en dos días hábiles.

## 6. Cambios

Podemos actualizar esta política. Cuando un cambio restrinja de forma sustancial lo que
está permitido, te damos un preaviso de
30 días
antes de que surta efecto, salvo cuando se requiera un plazo más corto para detener un
daño en curso o para cumplir la ley.

---

**Contacto:** hello@postqron.com
**Operado por:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
