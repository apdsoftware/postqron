---
document: terms-of-service
version: 1.2.0
effective_date: 2026-08-18
language: es
status: pending-review
---

# Términos del servicio

Estos términos rigen tu uso de Postqron. Al crear una cuenta los aceptas, junto con la
[Política de uso aceptable](acceptable-use-policy.md) y la
[Política de privacidad](privacy-policy.md).

## 1. Con quién contratas

Postqron está operado por
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
(«nosotros»).

**Las compras se realizan a través de Paddle.** Paddle actúa como Merchant of Record:
cuando compras un plan de pago, el contrato de venta de esa compra se celebra entre tú y
Paddle, y a él se aplican los propios términos de compra de Paddle además de los
presentes. Paddle se ocupa del pago, la facturación y los impuestos. Nosotros nos
ocupamos del servicio.

## 2. Qué hace el servicio

Postqron ejecuta peticiones HTTP a las direcciones que configures, en los momentos que
configures, registra el resultado y te avisa de los fallos. Las programaciones pueden
definirse en la aplicación o en un archivo `cron.yaml` dentro de un repositorio que
conectes.

**Postqron no ejecuta tu código.** Realiza peticiones HTTP. Si una petición desencadena
trabajo en tus sistemas, ese trabajo es tuyo.

## 3. Tu cuenta

Eres responsable de lo que ocurra bajo tu cuenta, de mantener seguras tus credenciales y
de las personas a las que invites a tu espacio de trabajo. Avísanos sin demora si crees
que tu cuenta se ha visto comprometida.

Debes tener al menos 16 años y, si actúas por cuenta de una organización, estar autorizado
para obligarla.

**El plan gratuito está abierto a cualquiera.** Úsalo para un proyecto personal, para
probar el servicio, o porque es suficiente para lo que necesitas. Nada de lo aquí escrito
te exige ser una empresa para crear una cuenta.

**Los planes de pago se ofrecen para uso profesional.** Al comprar uno, confirmas que
actúas en el marco de una actividad comercial, empresarial, artesanal o profesional. Por
eso nuestros precios se muestran sin impuestos: para quien tiene una actividad, la cifra
neta es la que importa, porque es la que deduce. Te pedimos que lo confirmes al pagar, y
recogemos tu número de IVA cuando lo tengas — algunos regímenes de pequeña empresa
perfectamente legítimos en Europa no lo emiten, así que lo pedimos, no lo exigimos.

Cuando la ley te reconozca protecciones de consumidor pese a esa confirmación, gana la ley
— incluido el derecho de desistimiento del §4.3.

## 4. Planes, límites y pago

Los planes, precios y límites son los publicados en nuestra página de precios y aplicados
por el servicio. **Los límites los impone el motor**, no se limitan a estar enunciados: el
número de trabajos de un plan, el intervalo mínimo y la conservación de los registros son
techos reales.

Los precios se muestran **sin impuestos**. Paddle calcula y añade el impuesto aplicable
según dónde te encuentres.

Los planes de pago se renuevan automáticamente por el mismo periodo hasta su cancelación.
Puedes cancelar en cualquier momento; la cancelación surte efecto al final del periodo que
ya has pagado, y hasta entonces el servicio continúa.

### 4.1 Cambio de plan

Las mejoras de plan surten efecto de inmediato. **Las reducciones de plan surten efecto al
final del periodo en curso**, y te decimos qué va a pasar antes de que confirmes.

**Si tienes más trabajos activos de los que permite el plan inferior, los pausamos todos y
eliges tú cuáles reactivar**, hasta el nuevo límite. No elegimos por ti, porque no
podemos: dos trabajos que a nosotros nos parecen idénticos pueden ser, para ti, uno que
emite facturas y otro que envía un recordatorio. Cualquier regla automática que nos
inventáramos adivinaría — y se equivocaría justo donde más importa.

Si tus trabajos activos ya caben en el nuevo límite, no se pausa nada.

**No borramos tu trabajo.** Los trabajos pausados siguen visibles, editables y
exportables, con su historial de ejecuciones. Una cosa que conviene saber: un trabajo
programado con más frecuencia de la que permite el nuevo plan no puede reactivarse hasta
que cambies su programación, aunque haya sitio para él.

Lo mismo se aplica si un pago falla definitivamente o si una suscripción decae: ambos
casos llevan la cuenta al plan gratuito.

### 4.2 Pago fallido

Si un pago falla, Paddle lo reintenta según su propio calendario. Durante ese periodo tu
servicio continúa. Si el pago falla definitivamente, la cuenta pasa al plan gratuito y el
§4.1 se aplica sin cambios: si tienes más trabajos activos de los que permite el plan
gratuito, se pausan todos y eliges tú cuáles reactivar. No se borra nada.

### 4.3 Reembolsos y desistimiento

La regla es sencilla: **puedes parar cuando quieras, y el mes que ya has pagado llega a su
fin.** No se reembolsa nada a prorrata, y no hay nada que reclamar ni que negociar.

Si eres consumidor en la Unión Europea, tienes además el derecho legal a desistir en los
14 días siguientes a la compra. Como el servicio empieza de inmediato, se te pide que
consientas su ejecución inmediata; ese consentimiento extingue el derecho de desistimiento
una vez que el servicio se ha ejecutado por completo. Cuando la ley nos obligue de todos
modos a reembolsarte, lo hacemos, y Paddle tramita el reembolso.

## 5. Disponibilidad

Aspiramos a mantener el servicio funcionando de forma continua, y te avisaremos cuando no
lo esté (consulta la Política de uso aceptable sobre cómo te contactamos ante
incidencias).

**No ofrecemos una garantía de disponibilidad, y queremos ser francos sobre por qué.** El
planificador y la base de datos se ejecutan en un único servidor, elegido deliberadamente
para que el envío no se retrase por la latencia de red. Esa elección cambia resiliencia
por precisión. Hacemos copias de seguridad y probamos su restauración, pero un fallo de
esa máquina interrumpe el servicio. Cualquier compromiso que asumiéramos más allá de lo
que una sola máquina puede dar sería un compromiso que no podríamos cumplir.

Si algún día ofrecemos un acuerdo de nivel de servicio con compromisos medibles, aparecerá
aquí — y la arquitectura habrá cambiado antes, no después.

## 6. Tus contenidos y los nuestros

**Lo tuyo sigue siendo tuyo.** Tus programaciones, tu configuración, tus registros y los
datos que haces pasar por el servicio siguen siendo de tu propiedad. Nos concedes solo el
permiso que necesitamos para operar el servicio para ti: almacenar esos datos, ejecutar
las peticiones que configuras y mostrarte los resultados.

Postqron en sí — el software, la interfaz, el nombre y la marca — sigue siendo nuestro.
Estos términos te dan derecho a usar el servicio, no a copiarlo ni a revenderlo.

## 7. Suspensión y terminación

Podemos suspender o cerrar tu cuenta por un incumplimiento sustancial de estos términos o
de la Política de uso aceptable, en la forma y con el preaviso allí descritos.

Puedes cerrar tu cuenta en cualquier momento. Al cerrarla detenemos la ejecución,
revocamos las claves y borramos tus datos tras el periodo de gracia indicado en la
Política de privacidad.

**Cerrar tu cuenta no cancela una suscripción de pago.** El pago lo gestiona Paddle como
Merchant of Record (§1), de modo que una suscripción se cancela ante Paddle, no ante
nosotros. Si cierras tu cuenta mientras hay un plan de pago en curso, el periodo que ya
has pagado llega a su fin, tal como se describe en el §4.3. Te lo decimos antes de que
confirmes, y te pedimos que lo reconozcas.

## 8. Responsabilidad

Nada de lo aquí escrito limita la responsabilidad que la ley no permite limitar, incluida
la responsabilidad por muerte o daños personales causados por negligencia, por dolo, o los
derechos que asisten a los consumidores en virtud de normas imperativas.

Sin perjuicio de lo anterior: prestamos el servicio con la diligencia y pericia
razonables, pero no respondemos de daños indirectos o consecuenciales, de la pérdida de
beneficios o de negocio, ni de las consecuencias del trabajo que tus trabajos desencadenan
en tus propios sistemas. **Una petición programada no es una garantía de que el trabajo
que hay detrás haya tenido éxito**, y deberías diseñar tus sistemas partiendo de esa
premisa.

Más allá de esas excepciones, **nuestra responsabilidad queda excluida en la máxima medida
permitida por la legislación aplicable**.

Preferimos decirlo con claridad antes que enterrarlo: Postqron es un planificador cuyo
precio va de cero a unas pocas decenas de euros al mes, y no puede soportar el riesgo de
aquello que depende de los trabajos que ejecuta. Si una ejecución omitida o duplicada te
causara un perjuicio relevante, el servicio no es el lugar adecuado para colocar esa
dependencia, y ninguna redacción de este documento cambia esa realidad de ingeniería.

## 9. Cambios en estos términos

Podemos modificar estos términos. Cuando un cambio afecte de forma sustancial a tus
derechos, te damos un preaviso de
30 días.
Si no aceptas el cambio, puedes cerrar tu cuenta antes de que surta efecto.

## 10. Ley aplicable y jurisdicción

Estos términos se rigen por
la ley italiana.
Los litigios se someten a la jurisdicción exclusiva de
los tribunales de Bérgamo, Italia,
**salvo** que, si eres consumidor, conservas la protección de las normas imperativas del
país en el que resides y puedes acudir a tus tribunales locales.

---

**Contacto:** hello@postqron.com
**Operado por:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
