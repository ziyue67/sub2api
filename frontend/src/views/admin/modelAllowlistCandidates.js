export const createModelAllowlistCandidatesTracker = () => {
    let currentRequestID = 0;
    const currentByMode = {};
    return {
        next(request) {
            currentRequestID += 1;
            currentByMode[request.mode] = {
                id: currentRequestID,
                request: { ...request },
            };
            return currentRequestID;
        },
        isCurrent(requestID, request) {
            const current = currentByMode[request.mode];
            return (current?.id === requestID &&
                current.request.groupID === request.groupID &&
                current.request.platform === request.platform);
        },
    };
};
