---
document: subprocessors
locale: es
version: "0.1"
title: "Registro de subencargados de Postqron"
controllerName: "APDSoftware — operador de Postqron"
contactEmail: help@postqron.com
status: draft_pending_legal_review
changeType: material
revisionSummary: "Borrador inicial redactado desde cero, pendiente de revisión legal."
---

## Identidad del proveedor

Este registro es emitido por APDSoftware, operador de Postqron, contactable en help@postqron.com y a través de https://apdsoftware.it. La denominación social completa, el domicilio social y el número de identificación fiscal de la entidad contratante registrada se registran como metadatos pendientes de revisión legal y se indicarán aquí en cuanto se confirmen.

## Finalidad de este registro

Este es el registro público, actualizado periódicamente, de los subencargados y otros terceros que Postqron contrata para prestar su servicio, al que se hace referencia desde los Términos de Servicio, la Política de Privacidad y el Acuerdo de Tratamiento de Datos, en lugar de duplicarse en dichos documentos. Distingue a los proveedores que actúan como nuestros subencargados en virtud del artículo 28 del RGPD (tratamiento de datos personales siguiendo instrucciones de Postqron) de los terceros independientes (como los proveedores de identidad OAuth) que actúan como responsables propios respecto de la fase del servicio que realizan. Cada entrada que figura a continuación se elabora únicamente a partir de fuentes primarias y oficiales citadas mediante URL, con la fecha en que se consultó cada fuente. Cuando un hecho no ha podido verificarse frente a una fuente oficial, esa carencia se indica de forma expresa en lugar de completarse.

La incorporación o sustitución de un subencargado que vaya a tratar datos de contenido de los clientes sigue el procedimiento de notificación y objeción descrito en el Acuerdo de Tratamiento de Datos: un preaviso de al menos 30 días a los propietarios (Owners) del espacio de trabajo, un canal para plantear una objeción motivada y la suspensión de la activación para el cliente que se oponga hasta que se resuelva la objeción. Debajo de la tabla activa se mantiene un historial de los proveedores retirados una vez que se da de baja a alguno.

## Subencargados y terceros activos

| Denominación legal | Función | Servicio | Categorías de datos | Establecimiento | Ubicación del tratamiento | Mecanismo de transferencia | Referencia al DPA | Fuente (consultada el 2026-07-25) |
|---|---|---|---|---|---|---|---|---|
| Paddle.com Market Limited (entidad contratante); Paddle.com Inc. (encargado según el DPA); Paddle Payments Limited; Paddle.com Canada Ltd | Subencargado | Procesamiento de pagos y facturación como Merchant of Record | Datos de contacto para facturación; metadatos de suscripción/transacción | Reino Unido; Irlanda; Estados Unidos; Canadá | No revelado por Paddle; puede ser tratado por cualquier entidad del grupo Paddle | Cláusulas contractuales tipo | [Addendum de Tratamiento de Datos de Paddle](https://www.paddle.com/legal/data-processing-addendum) | [DPA de Paddle](https://www.paddle.com/legal/data-processing-addendum) |
| Hetzner Online GmbH | Subencargado | Infraestructura de alojamiento en la nube (cómputo, almacenamiento, copias de seguridad) | Datos de la cuenta; datos del espacio de trabajo y de contenido; copias de seguridad cifradas | Alemania | Unión Europea/EEE cuando se selecciona una ubicación de servidor en la UE, conforme a la preferencia de alojamiento UE/EEE-primero de Postqron | Tratamiento en la UE/EEE (sin transferencia a un tercer país cuando se utiliza una ubicación de la UE) | [Auftragsverarbeitungsvertrag (DPA) de Hetzner](https://www.hetzner.com/AV/DPA_en.pdf) | [DPA de Hetzner](https://www.hetzner.com/AV/DPA_en.pdf) |
| Cloudflare, Inc. | Subencargado | DNS, CDN, red perimetral (edge) y terminación TLS | Metadatos de red y tráfico; direcciones IP | Estados Unidos | Red perimetral global; puede tratar datos fuera del EEE, Suiza y el Reino Unido en función de los servicios configurados | Cláusulas contractuales tipo (también certificado bajo el EU-US Data Privacy Framework y Global CBPR) | [DPA para clientes de Cloudflare](https://www.cloudflare.com/cloudflare-customer-dpa/) | [DPA de Cloudflare](https://www.cloudflare.com/cloudflare-customer-dpa/) |
| No verificado ("Mailronix") | Subencargado | Envío de correos electrónicos transaccionales (notificaciones de cuenta, seguridad y servicio) | Dirección de correo electrónico del destinatario; nombre del destinatario; contenido del mensaje transaccional | No verificado | No verificado | No aplicable — sin fuente verificada | No disponible | Solo contrato de API interno (`features/f14-email/contracts/mailronix-api-1.0.0.md`); no es una fuente legal pública |
| Google LLC; Google Ireland Limited | Tercero independiente | Inicio de sesión OAuth ("Iniciar sesión con Google") | Dirección de correo electrónico; nombre mostrado; foto de perfil; identificador de la cuenta de Google | Estados Unidos; Irlanda | Global | EU-US y Swiss-US Data Privacy Framework; cláusulas contractuales tipo cuando el Framework no resulte aplicable | No aplicable — no se ha publicado un DPA específico para esta funcionalidad | [Términos de Servicio de las API de Google](https://developers.google.com/terms) |
| Apple Inc. | Tercero independiente | Inicio de sesión OAuth ("Iniciar sesión con Apple") | Dirección de correo electrónico (o correo de reenvío privado de Apple); nombre (solo en el primer inicio de sesión); identificador de la cuenta de Apple | Estados Unidos | No verificado | No verificado | No aplicable — no se ha publicado un DPA específico para esta funcionalidad | [Iniciar sesión con Apple y privacidad](https://www.apple.com/legal/privacy/data/en/sign-in-with-apple/) |
| Meta Platforms, Inc.; Meta Platforms Ireland Limited | Tercero independiente | Inicio de sesión OAuth ("Facebook Login") y la conexión propia del cliente a Páginas de Facebook / Instagram Professional como destino de publicación | Dirección de correo electrónico; nombre; foto de perfil; identificador de la cuenta de Facebook/Instagram; contenido que el cliente decide publicar en su cuenta conectada | Irlanda; Estados Unidos | No verificado | Cláusulas contractuales tipo; Meta Platforms, Inc. también certificada bajo el Data Privacy Framework | No aplicable — no se ha publicado un DPA específico para esta funcionalidad | [Términos de la Plataforma de Meta](https://developers.facebook.com/terms/dfc_platform_terms/) |
| LinkedIn Corporation; LinkedIn Ireland Unlimited Company | Tercero independiente | Inicio de sesión OAuth ("Iniciar sesión con LinkedIn") | Dirección de correo electrónico; nombre; foto de perfil; identificador de la cuenta de LinkedIn | Estados Unidos; Irlanda | Estados Unidos | Cláusulas contractuales tipo; LinkedIn Corporation también certificada bajo el Data Privacy Framework | Solo referencia cruzada — el DPA de Desarrollo de Negocio de LinkedIn está enlazado desde sus Términos de Uso de la API, pero no menciona expresamente esta funcionalidad | [Términos de Uso de la API de LinkedIn](https://www.linkedin.com/legal/l/api-terms-of-use) |

## Carencias conocidas que deben resolverse antes de la publicación

- **No fue posible verificar "Mailronix".** Mediante investigación de fuentes primarias no se pudo localizar ningún sitio web oficial, página legal, DPA o lista de subencargados correspondiente a una empresa real que opere bajo este nombre. Esta entrada no puede publicarse como aprobada hasta que la función de gestión de proveedores de Postqron confirme la entidad legal contratante exacta y aporte su documentación oficial.
- **Las declaraciones sobre la ubicación del tratamiento de Apple y Meta** no se encontraron en las páginas oficiales revisadas y requieren confirmación directa antes de la publicación.
- **La aplicabilidad del DPA de Desarrollo de Negocio de LinkedIn al inicio de sesión OAuth en particular** se infiere únicamente por referencia cruzada y debería confirmarse directamente con LinkedIn o con el asesor legal.
- **La lista de subencargados del Trust Center de Paddle** (una página renderizada mediante JavaScript) no pudo leerse mediante investigación automatizada y su contenido no está verificado de forma independiente; el propio texto del DPA hace referencia además a un enlace obsoleto a una lista heredada de subencargados que debería aclararse con Paddle.

## Subencargados retirados

Ninguno registrado a la fecha de esta revisión del borrador.

## Contacto

Las consultas sobre este registro, o las objeciones a un subencargado incluido en virtud del Acuerdo de Tratamiento de Datos, deben dirigirse a help@postqron.com.
