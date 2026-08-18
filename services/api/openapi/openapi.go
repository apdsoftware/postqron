// Package openapi porta con sé il contratto dell'API pubblica (R51).
//
// # Perché il documento sta nel modulo e non in docs/
//
// Perché è **versionato insieme al codice**, che è ciò che R51 chiede, e perché
// da qui è compilato dentro il binario: un file che il compilatore include è un
// file che non si può cancellare o rinominare per sbaglio, e che si può servire
// da una rotta il giorno in cui serve. Un documento in una cartella di
// documentazione è un file che nessuno costruisce e che nessuno rompe.
//
// # Che cosa impedisce al documento di mentire
//
// Un contratto scritto a mano diverge dal codice entro un mese, e da quel
// momento è peggio di non averlo: chi lo legge costruisce un client su una
// promessa falsa, e il difetto si manifesta a casa sua. La difesa è un controllo
// automatico che confronta questo file con il codice a ogni corsa di `make ci` —
// rotte, permessi, codici di errore, campi delle risposte ed enumerazioni. Sta
// in internal/httpapi/contract_test.go, accanto al codice che descrive, perché è
// quel codice che deve poterlo far fallire.
package openapi

import _ "embed"

// Spec è il documento OpenAPI 3.1 dell'API pubblica, in YAML.
//
// È esposto come byte e non come struttura decodificata perché il consumatore
// naturale è chi lo serve o chi lo valida, non chi lo interroga: una struttura
// obbligherebbe questo package a scegliere un modello del formato, cioè a
// diventare una libreria OpenAPI.
//
//go:embed openapi.yaml
var Spec []byte
