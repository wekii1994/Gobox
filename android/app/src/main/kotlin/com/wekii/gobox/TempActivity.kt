package com.wekii.gobox

import android.app.Activity
import android.os.Bundle
import com.wekii.gobox.extensions.wrapAction
import com.wekii.gobox.widget.HomeWidgetRenderer
import com.wekii.gobox.widget.HomeWidgets

class TempActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        when (intent.action) {
            wrapAction("START") -> GlobalState.handleStart()
            wrapAction("STOP") -> GlobalState.handleStop()
            wrapAction("CHANGE") -> GlobalState.handleToggle()
            wrapAction(HomeWidgetRenderer.REFRESH_ACTION) -> HomeWidgets.refresh(this)
        }
        finishAndRemoveTask()
    }
}
