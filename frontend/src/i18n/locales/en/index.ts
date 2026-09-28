import priorityScheduling from './priorityScheduling'
import qualityOps from './qualityOps'
import accountOps from './accountOps'
import tokenGuard from './tokenGuard'
import pelicanTests from './pelicanTests'
import tokenGuardV2 from './tokenGuardV2'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import misc from './misc'
import leaderboard from './leaderboard'
import chatImageStudio from './chatImageStudio'
import imageCreator from './imageCreator'
import imageManager from './imageManager'
import canvas from './canvas'
import chatStudio from './chatStudio'

import requestTiming from './requestTiming'

export default {
  priorityScheduling,
  qualityOps,
  accountOps,
  tokenGuard,
  pelicanTests,
  tokenGuardV2,
  requestTiming,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  admin,
  ...misc,
  leaderboard,
  chatImageStudio,
  imageCreator,
  imageManager,
  canvas,
  chatStudio,
}
