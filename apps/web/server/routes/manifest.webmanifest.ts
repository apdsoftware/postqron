import { defineEventHandler, setHeader } from 'h3'
import { readFeatureAsset } from '../utils/feature-asset'

export default defineEventHandler(async (event) => {
  setHeader(event, 'content-type', 'application/manifest+json; charset=utf-8')
  setHeader(event, 'cache-control', 'public, max-age=3600')
  return readFeatureAsset('f23-pwa', 'web/manifest.webmanifest')
})
