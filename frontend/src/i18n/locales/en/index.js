import profitOps from './profitOps';
import upstreamOps from './upstreamOps';
import channelOps from './channelOps';
import ipAudit from './ipAudit';
import traffic from './traffic';
import connections from './connections';
import securityRecords from './securityRecords';
import siwc from './siwc';
import priorityScheduling from './priorityScheduling';
import qualityOps from './qualityOps';
import controlledExperiments from './controlledExperiments';
import accountOps from './accountOps';
import tokenGuard from './tokenGuard';
import pelicanTests from './pelicanTests';
import modelTrace from './modelTrace';
import tokenGuardV2 from './tokenGuardV2';
import adminAccessKeys from './adminAccessKeys';
import landing from './landing';
import common from './common';
import dashboard from './dashboard';
import channelMonitorV2 from './channelMonitorV2';
import channelMonitorV3 from './channelMonitorV3';
import supportTickets from './supportTickets';
import batchImage from './batchImage';
import admin from './admin';
import misc from './misc';
import leaderboard from './leaderboard';
import chatImageStudio from './chatImageStudio';
import imageCreator from './imageCreator';
import imageManager from './imageManager';
import imageArchive from './imageArchive';
import canvas from './canvas';
import chatStudio from './chatStudio';
import requestTiming from './requestTiming';
import autoConfig from './autoConfig';
export default {
    traffic,
    ipAudit,
    connections,
    securityRecords,
    siwc,
    autoConfig,
    priorityScheduling,
    qualityOps,
    channelOps,
    upstreamOps,
    profitOps,
    controlledExperiments,
    accountOps,
    tokenGuard,
    pelicanTests,
    modelTrace,
    tokenGuardV2,
    adminAccessKeys,
    requestTiming,
    ...landing,
    ...common,
    ...dashboard,
    ...channelMonitorV2,
    ...channelMonitorV3,
    ...supportTickets,
    ...batchImage,
    admin,
    ...misc,
    leaderboard,
    chatImageStudio,
    imageCreator,
    imageManager,
    imageArchive,
    canvas,
    chatStudio,
};
