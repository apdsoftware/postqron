import { defineEventHandler, setHeader } from 'h3'
import { readFeatureAsset } from '../utils/feature-asset'

export default defineEventHandler(async (event) => {
  setHeader(event, 'content-type', 'text/javascript; charset=utf-8')
  setHeader(event, 'cache-control', 'no-cache')
  return readFeatureAsset('f23-pwa', 'web/service-worker.js')
})
