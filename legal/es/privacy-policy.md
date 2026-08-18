---
document: privacy-policy
version: 1.1.0
effective_date: 2026-08-18
language: es
status: pending-review
---

# Política de privacidad

Esta política explica qué datos personales trata Postqron, por qué, y qué puedes hacer al
respecto. Está escrita para leerse, no para sobrevivirla.

## 1. Quién es el responsable

El responsable del tratamiento de tus datos personales es
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224.

Puedes escribirnos a
privacy@postqron.com.

No hemos nombrado un delegado de protección de datos: nuestro tratamiento no cumple las
condiciones del art. 37 del RGPD — no somos una autoridad pública, nuestra actividad
principal no es la observación sistemática a gran escala, y no tratamos categorías
especiales de datos a gran escala. Las solicitudes en materia de privacidad se dirigen a
la dirección anterior y las atendemos nosotros directamente.

## 2. Qué tratamos, y por qué

### 2.1 Cuenta y autenticación

Dirección de correo electrónico, contraseña (almacenada solo como hash Argon2id — nunca
guardamos la contraseña en sí), idioma preferido, sesiones y su caducidad, y los tokens
usados para verificar tu dirección o restablecer tu contraseña.

**Por qué:** para prestar el servicio que has pedido. **Base jurídica:** ejecución de un
contrato (art. 6.1.b del RGPD).

### 2.2 Trabajos y ejecuciones

Las programaciones que defines, las direcciones de destino, los métodos HTTP, las
cabeceras y los cuerpos que configuras, y para cada ejecución: la hora de inicio y de fin,
su duración, el resultado, el estado HTTP, un extracto truncado de la respuesta y el
número de intento.

Dos cosas conviene decirlas sin rodeos. Primero, **tú decides qué entra en un trabajo**:
si pones datos personales en una URL, en una cabecera o en un cuerpo, los trataremos
porque los has puesto tú. Segundo, **los extractos de las respuestas se almacenan**, así
que si el sistema al que llamas devuelve datos personales, esos datos llegan a nuestros
registros.

**Por qué:** para prestar el servicio y para que puedas ver qué ha pasado. **Base
jurídica:** ejecución de un contrato.

**Conservación:** los registros de ejecución se guardan durante el periodo de tu plan — 3,
15, 30 o 90 días — y después se borran.

### 2.3 Sincronización de repositorios

Si conectas un repositorio de GitHub, tratamos el identificador del repositorio, los
eventos que GitHub nos envía cuando haces push y el contenido del archivo `cron.yaml`.
Solicitamos acceso de solo lectura al contenido y a los metadatos del repositorio, y a
nada más.

**Base jurídica:** ejecución de un contrato.

### 2.4 Secretos y credenciales

Los secretos del espacio de trabajo, las claves de API y las claves de proveedores de IA
se cifran en reposo, nunca se devuelven en forma legible después de guardarse y nunca se
escriben en los registros.

### 2.5 Facturación

Los pagos los gestiona Paddle como Merchant of Record (§4). Recibimos el estado de la
suscripción, el plan y los identificadores necesarios para conciliarla. **Nunca vemos tu
tarjeta de pago.**

**Base jurídica:** ejecución de un contrato y obligación legal para los registros
fiscales.

### 2.6 Seguridad y auditoría

Registros de eventos sensibles: inicios de sesión, cambios de plan, revocación de claves,
suplantación administrativa. Los registros técnicos están estructurados para excluir
secretos y datos personales que no sean necesarios.

**Base jurídica:** interés legítimo en operar un servicio seguro (art. 6.1.f), y
obligación legal cuando proceda.

### 2.7 Correo transaccional

Te enviamos el correo que necesitas para usar el servicio: bienvenida, avisos de trabajos
fallidos, cambios de plan, eventos de seguridad. No son marketing y no puedes darte de
baja de ellos sin cerrar tu cuenta, porque son la forma en que el servicio te cuenta las
cosas.

### 2.8 Correo de marketing

Si lo aceptas, te enviamos correo sobre el producto: nuevas funciones, cambios que merece
la pena conocer, de vez en cuando algo que hemos escrito.

**Está separado del correo anterior en todos los aspectos.** La base jurídica es tu
**consentimiento** (art. 6.1.a), solicitado por sí mismo y nunca agrupado con la
aceptación de los términos o la creación de una cuenta. Rechazarlo no te cuesta nada: el
servicio funciona igual.

Cada mensaje de marketing lleva un enlace de baja que funciona con un clic y sin iniciar
sesión. Darse de baja detiene únicamente el correo de marketing — sigues recibiendo el
correo transaccional que el servicio necesita enviarte, porque eso no es marketing.

Conservamos constancia de cuándo diste tu consentimiento y de cuándo lo retiraste, que es
como podemos demostrar que teníamos derecho a escribirte.

## 3. Funciones de IA: una transferencia que conviene entender

Si activas la depuración asistida por IA, aportas **tu propia** clave de API de un
proveedor de IA (OpenAI, Anthropic u otro). Cuando usas la función, el contenido del
registro de ejecución que estás analizando se envía a ese proveedor con tu clave y bajo
sus condiciones.

Esto significa que tus datos salen de nuestra infraestructura y llegan a un tercero **que
has elegido tú**, en virtud de un contrato **entre tú y él**. Nosotros no somos parte, no
controlamos qué hace con el contenido, y se aplican sus normas de conservación, no las
nuestras.

La función está desactivada hasta que la actives, y cada análisis es un acto deliberado.
Pedimos tu consentimiento explícito antes de la primera transferencia.

**Base jurídica:** consentimiento (art. 6.1.a), que puedes retirar en cualquier momento
eliminando tu clave. La retirada no afecta a las transferencias ya realizadas.

## 4. Quién más trata tus datos

Recurrimos a estos proveedores. Cada uno trata los datos siguiendo nuestras instrucciones,
al amparo de un contrato de encargo del tratamiento.

| Proveedor | Función | Dónde |
|---|---|---|
| Hetzner | Servidores y base de datos | Alemania |
| Cloudflare | DNS, TLS, CDN, alojamiento estático, protección perimetral | Red edge global |
| Paddle | Merchant of Record: pagos, facturación, impuestos | Reino Unido |
| Mailronix | Entrega de correo transaccional | Unión Europea — operado por Apdsoftware, la misma entidad que opera Postqron |
| GitHub | Sincronización de repositorios, solo si conectas uno | Estados Unidos |

Mantenemos esta lista al día. Si añadimos o cambiamos un proveedor de un modo que te
afecte, actualizamos esta política y, cuando el cambio sea sustancial, te lo decimos antes
de que surta efecto.

**Transferencias fuera del EEE.** Algunos proveedores tratan datos fuera del Espacio
Económico Europeo. Cuando ocurre, nos apoyamos en las garantías del art. 46 del RGPD,
principalmente las Cláusulas Contractuales Tipo de la Comisión Europea, junto con las
medidas técnicas del propio proveedor.

## 5. Cuánto tiempo conservamos las cosas

| Dato | Conservado |
|---|---|
| Cuenta y perfil | Mientras exista la cuenta |
| Registros de ejecución | 3, 15, 30 o 90 días, según el plan |
| Registros de auditoría | 24 meses |
| Registros contables y fiscales | Lo que exija la ley, normalmente 10 años |
| Copias de seguridad | 30 días |

Cuando borras tu cuenta detenemos la ejecución y revocamos las claves de inmediato, y
después eliminamos los datos tras un periodo de gracia de
30 días,
durante el cual puedes cambiar de opinión. Los datos ya escritos en copias de seguridad
desaparecen a medida que esas copias rotan. Sobreviven al borrado únicamente los registros
que debemos conservar por razones fiscales o legales.

Una cosa sobrevive al borrado sin seguir tratando sobre ti. Cuando un administrador ha
actuado sobre tu cuenta, nuestro registro de seguridad conserva constancia de lo que hizo
**él**, con toda referencia a ti eliminada. Lo que queda dice que hubo una acción y quién
la realizó; ya no dice sobre quién. Lo conservamos porque, de lo contrario, cerrar una
cuenta borraría la prueba del acceso de otra persona a ella. No es un registro que
guardemos por razones fiscales o legales — es un registro de seguridad sobre los actos de
otra persona.

## 6. Tus derechos

Puedes pedirnos una copia de tus datos, su rectificación, su supresión, la limitación del
tratamiento u oponerte a él, o que te los facilitemos en un formato portable. Puedes
retirar el consentimiento cuando el tratamiento se base en el consentimiento.

La exportación y la supresión están disponibles en la aplicación sin necesidad de
pedírnoslo. Para todo lo demás, escríbenos y responderemos en el plazo de un mes.

Si crees que estamos tratando tus datos indebidamente, puedes reclamar ante la autoridad
de control de tu país. En Italia es el *Garante per la protezione dei dati personali*.

## 7. Seguridad

Ciframos los secretos en reposo, aplicamos hash a las contraseñas con Argon2id, mantenemos
los registros libres de credenciales, verificamos la firma de los webhooks entrantes,
limitamos la frecuencia de la autenticación y anotamos los eventos sensibles en un
registro de auditoría.

También deberíamos contarte lo que no tenemos: Postqron se ejecuta en un único servidor,
elegido deliberadamente para que el planificador y la base de datos estén uno junto al
otro. Esa elección cambia resiliencia por latencia. Hacemos copias de seguridad y hemos
probado su restauración, pero un fallo de esa máquina interrumpe el servicio.

## 8. Decisiones automatizadas

No tomamos decisiones con efectos jurídicos o similarmente significativos sobre ti por
medios automatizados, y no te elaboramos perfiles.

## 9. Menores

Postqron no está destinado a personas menores de
16 años.
No recogemos sus datos conscientemente.

## 10. Cambios

Podemos actualizar esta política. La versión y la fecha de entrada en vigor están al
principio. Cuando un cambio sea sustancial te lo decimos antes de que surta efecto y,
cuando la ley lo exija, te pedimos de nuevo tu consentimiento.

---

**Contacto:** privacy@postqron.com
**Operado por:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
